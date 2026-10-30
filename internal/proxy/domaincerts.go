// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/fileutil"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// Per-domain certificate layout, mirroring legacy ProxyConfGen.java
// (DOMAIN_SSL_DIR line 124, SSL_CRT_EXT/SSL_KEY_EXT lines 98-99).
const (
	domainCertsDirName = "domaincerts"
	sslCrtExt          = ".crt"
	sslKeyExt          = ".key"
	sslClientCAExt     = ".client.ca.crt"

	// domainCertFileMode is the mode of the per-domain cert/key files. Legacy
	// wrote them with FileOutputStream, i.e. the process umask (027 for the
	// zextras user), so they are never world-readable.
	domainCertFileMode = 0o640

	domainCertsDirMode = 0o755
)

// domainCertsDir returns the directory holding per-domain cert/key files.
func (g *Generator) domainCertsDir() string {
	return filepath.Join(g.ConfDir, domainCertsDirName)
}

// domainCertFilePath returns <conf>/domaincerts/<domain><ext>. The domain
// name has been validated by validDomainName before it reaches here.
func (g *Generator) domainCertFilePath(domainName, ext string) string {
	return filepath.Join(g.domainCertsDir(), domainName+ext)
}

// domainSSLPathOrDefault maps a domain's SSL PEM attribute to the nginx file
// path: <conf>/domaincerts/<domain><ext> when the domain carries PEM content,
// otherwise the global default (legacy ProxyConfGen.java:794-806).
func (g *Generator) domainSSLPathOrDefault(domainName, pem, ext string, defaultVar *Variable) string {
	if pem != "" {
		return g.domainCertFilePath(domainName, ext)
	}

	if defaultVar != nil {
		if s, ok := defaultVar.Value.(string); ok {
			return s
		}
	}

	return ""
}

// syncDomainCerts is the per-domain certificate pipeline, run before templates
// are rendered. It follows the order of legacy ProxyConfGen.java:2216-2240:
//
//  1. load domains (per-vhn gated),
//  2. delete Let's Encrypt config of removed domains (deleteDomainCertbotConfiguration),
//  3. save Let's Encrypt cert/key pairs to LDAP (saveCertificateKeyPairFromCertbotWorkingDirToLdap),
//  4. deploy cert/key pairs to <conf>/domaincerts (downloadCertificateKeyPairToDomainCertDir),
//  5. write the combined client CA (loadAllClientCertCA + writeClientCAtoFile),
//  6. delete obsolete files in domaincerts (deleteObsoleteCertificateKeyPairFromDomainCertDir).
//
// Files are written atomically and only when their content changed, so an
// unchanged configuration produces no file churn and no proxy reload. When
// neither a domain provider nor an LDAP client exists the pipeline is skipped.
func (tp *TemplateProcessor) syncDomainCerts(ctx context.Context) error {
	domains, err := tp.loadDomains(ctx)
	if errors.Is(err, errNoDomainSource) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to load domains: %w", err)
	}

	g := tp.generator

	tp.deleteCertbotConfigs(ctx, domains)

	if domains, err = tp.saveCertbotPairsToLDAP(ctx, domains); err != nil {
		return err
	}

	if err := tp.deployDomainCerts(ctx, domains); err != nil {
		return err
	}

	if err := tp.syncClientCA(ctx, domains); err != nil {
		return err
	}

	tp.removeObsoleteDomainCerts(ctx, g.domainCertsDir(), domains)

	return nil
}

// deployDomainCerts writes <domain>.crt/.key for every domain holding both
// PEMs (legacy ProxyConfGen.java:2416-2431; dry-run only logs, :2421).
func (tp *TemplateProcessor) deployDomainCerts(ctx context.Context, domains []DomainInfo) error {
	g := tp.generator

	if g.DryRun {
		logger.InfoContext(ctx, "DRY-RUN: Will download certificate/key pairs to domain certificate directory")

		return nil
	}

	dir := g.domainCertsDir()
	if err := os.MkdirAll(dir, domainCertsDirMode); err != nil {
		return fmt.Errorf("unable to create folder %s: %w", dir, err)
	}

	for i := range domains {
		d := &domains[i]
		// Legacy only deploys a pair when both cert and key are present.
		if d.SSLCertificate == "" || d.SSLPrivateKey == "" {
			continue
		}

		if err := writeIfChanged(ctx, g.domainCertFilePath(d.Name, sslCrtExt), d.SSLCertificate); err != nil {
			return fmt.Errorf("unable to write certificate for domain %s: %w", d.Name, err)
		}

		if err := writeIfChanged(ctx, g.domainCertFilePath(d.Name, sslKeyExt), d.SSLPrivateKey); err != nil {
			return fmt.Errorf("unable to write private key for domain %s: %w", d.Name, err)
		}
	}

	return nil
}

// removeObsoleteDomainCerts deletes files of removed domains from dir, or only
// reports them in dry-run (legacy ProxyConfGen.java:2443-2463). Delete
// failures are logged and generation continues, as in legacy.
func (tp *TemplateProcessor) removeObsoleteDomainCerts(ctx context.Context, dir string, domains []DomainInfo) {
	dryRun := tp.generator.DryRun

	for _, path := range obsoleteDomainCertFiles(dir, domains) {
		if dryRun {
			logger.InfoContext(ctx, "DRY-RUN: Will delete obsoleted domain certificate file", "path", path)

			continue
		}

		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.WarnContext(ctx, "Unable to delete obsolete domain certificate file",
				"path", path, "error", err)

			continue
		}

		logger.InfoContext(ctx, "Deleted obsolete domain certificate file", "path", path)
	}
}

// writeIfChanged atomically writes content to path unless the file already
// holds exactly that content.
func writeIfChanged(ctx context.Context, path, content string) error {
	data := []byte(content)

	//nolint:gosec // G304: path is <conf>/domaincerts/<validated domain name>
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm() == domainCertFileMode {
			return nil
		}
	}

	if err := fileutil.AtomicWrite(path, data, domainCertFileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	logger.DebugContext(ctx, "Deployed domain certificate file", "path", path)

	return nil
}

// obsoleteDomainCertFiles lists regular files directly inside dir whose name
// does not contain any current domain name, mirroring legacy
// getFilesPathInDirectory + removeIf(path.contains(domainName)) (depth 1,
// regular files only). The match is made on the file name rather than the
// whole path so a domain called e.g. "conf" cannot shield every file.
// The result is sorted (os.ReadDir order) for deterministic logs.
func obsoleteDomainCertFiles(dir string, domains []DomainInfo) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var obsolete []string

	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}

		if domainsOwnFile(e.Name(), domains) {
			continue
		}

		obsolete = append(obsolete, filepath.Join(dir, e.Name()))
	}

	return obsolete
}

// domainsOwnFile reports whether fileName contains any domain's name.
func domainsOwnFile(fileName string, domains []DomainInfo) bool {
	for i := range domains {
		if strings.Contains(fileName, domains[i].Name) {
			return true
		}
	}

	return false
}
