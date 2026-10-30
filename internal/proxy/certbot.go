// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// Let's Encrypt integration, ported from legacy ProxyConfGen.java:
// CERTBOT (:120), CERTBOT_WORKING_DIR (:121-122), CERT/KEY (:110-111).
const (
	certbotBinaryRel     = "libexec/certbot"
	certbotLiveDirRel    = "common/certbot/etc/letsencrypt/live"
	certbotFullchainName = "fullchain.pem"
	certbotPrivkeyName   = "privkey.pem"

	// certbotDeleteTimeout bounds the certbot delete invocation. Legacy
	// started the process without waiting (ProxyConfGen.java:2517).
	certbotDeleteTimeout = 2 * time.Minute

	domainCacheKey = "ldap:domains"
)

func (g *Generator) certbotBinary() string {
	return filepath.Join(g.WorkingDir, certbotBinaryRel)
}

func (g *Generator) certbotLiveDir() string {
	return filepath.Join(g.WorkingDir, certbotLiveDirRel)
}

// certbotDomainDirs lists the subdirectories of the certbot live directory
// (legacy Utils.getSubdirectoriesNames, symlinks to directories included).
func (g *Generator) certbotDomainDirs() []string {
	if g.WorkingDir == "" {
		return nil
	}

	live := g.certbotLiveDir()

	entries, err := os.ReadDir(live)
	if err != nil {
		return nil
	}

	var names []string

	for _, e := range entries {
		if info, statErr := os.Stat(filepath.Join(live, e.Name())); statErr == nil && info.IsDir() {
			names = append(names, e.Name())
		}
	}

	return names
}

// deleteCertbotConfigs removes the Let's Encrypt configuration of domains that
// no longer exist (legacy deleteDomainCertbotConfiguration, ProxyConfGen.java:2478-2500).
// A certbot directory is kept when its name contains the name of any current
// domain (legacy domain.contains(entry.getDomainName())). certbot is run as
// `certbot delete <names...>` with argv, no shell. Failures are logged and do
// not stop generation (legacy :2492-2496).
func (tp *TemplateProcessor) deleteCertbotConfigs(ctx context.Context, domains []DomainInfo) {
	g := tp.generator

	stale := g.certbotDomainDirs()
	if len(stale) == 0 {
		return
	}

	stale = slicesDeleteOwned(stale, domains)

	// A directory name beginning with "-" would be parsed as a certbot option.
	kept := stale[:0]

	for _, name := range stale {
		if strings.HasPrefix(name, "-") {
			logger.WarnContext(ctx, "Ignoring certbot directory with option-like name", "name", truncateForLog(name))

			continue
		}

		kept = append(kept, name)
	}

	if len(kept) == 0 {
		return
	}

	if g.DryRun {
		for _, name := range kept {
			logger.InfoContext(ctx, "DRY-RUN: Will delete Let's Encrypt config for domain", "domain", name)
		}

		return
	}

	for _, name := range kept {
		logger.InfoContext(ctx, "Deleting Let's Encrypt configuration", "domain", name)
	}

	ctx, cancel := context.WithTimeout(ctx, certbotDeleteTimeout)
	defer cancel()

	args := append([]string{"delete"}, kept...)

	//nolint:gosec // G204: fixed binary under WorkingDir; names are directory entries passed as argv, no shell
	if out, err := exec.CommandContext(ctx, g.certbotBinary(), args...).CombinedOutput(); err != nil {
		logger.WarnContext(ctx, "Unable to delete Let's Encrypt configurations",
			"error", err, "output", truncateForLog(string(out)))
	}
}

// slicesDeleteOwned drops names that contain the name of any domain.
func slicesDeleteOwned(names []string, domains []DomainInfo) []string {
	kept := names[:0]

	for _, name := range names {
		if !domainsOwnFile(name, domains) {
			kept = append(kept, name)
		}
	}

	return kept
}

// saveCertbotPairsToLDAP copies Let's Encrypt fullchain/privkey of every
// certbot-managed domain into zimbraSSLCertificate/zimbraSSLPrivateKey and
// updates the in-memory domain list (legacy
// saveCertificateKeyPairFromCertbotWorkingDirToLdap, ProxyConfGen.java:2536-2546
// and updateCertificateKeyPair :2571-2606).
//
// A domain is certbot-managed when live/<domain> exists (:2557). The LDAP
// write is skipped when the stored values already equal the files, which
// avoids a no-op modify (and modifyTimestamp bump) on every run. Dry-run only
// logs (:2536-2537). Any failure aborts generation, as in legacy (:2603).
func (tp *TemplateProcessor) saveCertbotPairsToLDAP(
	ctx context.Context, domains []DomainInfo,
) ([]DomainInfo, error) {
	g := tp.generator

	if g.DryRun {
		logger.InfoContext(ctx, "DRY-RUN: Will save Let's Encrypt certificate/key pairs to LDAP")

		return domains, nil
	}

	if g.WorkingDir == "" {
		return domains, nil
	}

	saved := make(map[string]bool)
	changed := false

	for i := range domains {
		d := &domains[i]

		pair, ok := readCertbotPair(g.certbotLiveDir(), d.Name)
		if !ok {
			continue
		}

		if !saved[d.Name] && (d.SSLCertificate != pair.cert || d.SSLPrivateKey != pair.key) {
			if err := tp.writeDomainPairToLDAP(ctx, d.Name, pair); err != nil {
				return nil, err
			}

			changed = true
		}

		saved[d.Name] = true
		d.SSLCertificate = pair.cert
		d.SSLPrivateKey = pair.key
	}

	if changed && g.Cache != nil {
		// Later getDomains calls must see the values just written.
		g.Cache.InvalidateCacheByPrefix(domainCacheKey)
	}

	return domains, nil
}

type certbotPair struct{ cert, key string }

// readCertbotPair returns the PEM files of a certbot-managed domain; ok is
// false when the domain is not managed or either file is missing.
func readCertbotPair(liveDir, domainName string) (certbotPair, bool) {
	dir := filepath.Join(liveDir, domainName)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return certbotPair{}, false
	}

	//nolint:gosec // G304: domainName passed validDomainName; liveDir is fixed
	cert, certErr := os.ReadFile(filepath.Join(dir, certbotFullchainName))
	//nolint:gosec // G304: as above
	key, keyErr := os.ReadFile(filepath.Join(dir, certbotPrivkeyName))

	if certErr != nil || keyErr != nil {
		return certbotPair{}, false
	}

	return certbotPair{cert: string(cert), key: string(key)}, true
}

func (tp *TemplateProcessor) writeDomainPairToLDAP(ctx context.Context, domainName string, pair certbotPair) error {
	g := tp.generator
	if g.LdapClient == nil || g.LdapClient.NativeClient == nil {
		return fmt.Errorf("unable to update certificate and private key for domain %s: %w",
			domainName, errors.New("LDAP client not initialized"))
	}

	logger.InfoContext(ctx, "Saving Let's Encrypt certificate/key pair to LDAP", "domain", domainName)

	if err := g.LdapClient.NativeClient.SetDomainSSLCertKey(domainName, pair.cert, pair.key); err != nil {
		return fmt.Errorf("unable to update certificate and private key for domain %s: %w", domainName, err)
	}

	return nil
}
