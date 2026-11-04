// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Fixture is the input of an offline render: the configuration the generator
// would read from LDAP, given as data. It lets templates be rendered anywhere,
// without LDAP, localconfig or a Carbonio installation.
//
// The variables resolve as in production: every declared variable takes its
// value from the global and server attributes (or its default), and Variables
// then overrides single variables with the value the generator would hold.
type Fixture struct {
	// WorkDir is the installation directory the rendered paths refer to
	// (default /opt/zextras). Nothing is written there.
	WorkDir string `yaml:"workdir"`
	// Hostname is the server's hostname.
	Hostname string `yaml:"hostname"`
	// Global and Server are LDAP attributes of the global config and of the
	// server (zimbraReverseProxyMailEnabled: "TRUE"); Server wins.
	Global map[string]string `yaml:"global"`
	Server map[string]string `yaml:"server"`
	// Variables sets template variables directly, by name: a bool, a number, a
	// string or a list of strings, as the variable's value is typed.
	Variables map[string]any `yaml:"variables"`
	// Domains are the domains with a virtual hostname that
	// "!{explode domain(...)}" templates iterate over.
	Domains []FixtureDomain `yaml:"domains"`
	// Servers maps a service name to the servers running it, for
	// "!{explode server(service)}" templates.
	Servers map[string][]FixtureServer `yaml:"servers"`
}

// FixtureDomain is a domain of a Fixture. VirtualIP must be an IP address: an
// offline render does not resolve names.
type FixtureDomain struct {
	Name           string `yaml:"name"`
	VirtualHost    string `yaml:"vhn"`
	VirtualIP      string `yaml:"vip"`
	ClientCertMode string `yaml:"clientcertmode"`
	ClientCertCA   string `yaml:"clientcertca"`
	SSLCertificate string `yaml:"sslcertificate"`
	SSLPrivateKey  string `yaml:"sslprivatekey"`
}

// FixtureServer is a server of a Fixture.
type FixtureServer struct {
	ID       string `yaml:"id"`
	Hostname string `yaml:"hostname"`
}

// ParseFixture parses a YAML fixture; unknown fields are errors.
func ParseFixture(data []byte) (*Fixture, error) {
	var fx Fixture

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(&fx); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}

	return &fx, nil
}

// RenderOffline renders every template of templateDir with fx and writes the
// files the generator would write below outDir, which stands for the conf
// directory (outDir/nginx.conf, outDir/nginx/includes/...). Variables that
// hold paths still refer to the fixture's working directory. It returns the
// written files, relative to outDir.
func RenderOffline(
	ctx context.Context, env sys.System, fx *Fixture, templateDir, outDir string,
) ([]string, error) {
	templateDir, err := filepath.Abs(templateDir)
	if err != nil {
		return nil, err
	}

	g, err := fx.generator(ctx, env)
	if err != nil {
		return nil, err
	}

	templates, err := discoverTemplatesInDir(env.FS, templateDir)
	if err != nil {
		return nil, fmt.Errorf("find templates in %s: %w", templateDir, err)
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("no templates found in %s", templateDir)
	}

	slices.Sort(templates)

	tp := NewTemplateProcessor(g, env.FS, templateDir, g.IncludesDir)
	tp.domainProvider = fx.domainInfos
	tp.serverProvider = fx.serverInfos
	tp.hostResolver = literalHost

	var (
		written []string
		errs    []error
	)

	for _, path := range templates {
		name, ok := g.outputName(filepath.Base(path))
		if !ok {
			continue
		}

		rel, err := filepath.Rel(g.ConfDir, g.outputPath(name))
		if err != nil {
			return nil, err
		}

		if err := renderTemplate(ctx, tp, path, filepath.Join(outDir, rel)); err != nil {
			errs = append(errs, err)

			continue
		}

		written = append(written, filepath.ToSlash(rel))
	}

	return written, errors.Join(errs...)
}

func renderTemplate(ctx context.Context, tp *TemplateProcessor, templatePath, outPath string) error {
	t, err := tp.LoadTemplate(ctx, templatePath)
	if err != nil {
		return err
	}

	out, err := tp.ProcessTemplate(ctx, t)
	if err != nil {
		return fmt.Errorf("render %s: %w", templatePath, err)
	}

	if err := tp.fs.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}

	return tp.fs.WriteFileAtomic(outPath, []byte(out), generatedFileMode)
}

// generator builds the Generator for the fixture: every variable registered
// and resolved from the fixture's attributes, then the Variables overrides.
func (fx *Fixture) generator(ctx context.Context, env sys.System) (*Generator, error) {
	workDir := cmp.Or(fx.WorkDir, defaultWorkDir)

	src := Sources{
		Global: &config.GlobalConfig{Data: config.NewConfigMap()},
		Server: &config.ServerConfig{Data: config.NewConfigMap()},
	}
	src.Global.Data.Replace(fx.Global)
	src.Server.Data.Replace(fx.Server)

	g, err := NewGenerator(ctx, env, &config.Config{BaseDir: workDir, Hostname: fx.Hostname}, src)
	if err != nil {
		return nil, err
	}

	for _, name := range sortedKeys(fx.Variables) {
		v, ok := g.Variables[name]
		if !ok {
			return nil, fmt.Errorf("fixture: unknown variable %q", name)
		}

		val, err := fixtureValue(fx.Variables[name])
		if err != nil {
			return nil, fmt.Errorf("fixture: variable %q: %w", name, err)
		}

		v.Value = val
		v.ConfigText = nil

		if _, err := g.ExpandVariable(ctx, name); err != nil {
			return nil, fmt.Errorf("fixture: variable %q: %w", name, err)
		}
	}

	return g, nil
}

// fixtureValue converts a YAML value to the Go value of a Variable.
func fixtureValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, int, int64, string:
		return x, nil
	case []any:
		list := make([]string, len(x))

		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("list item %d is %T, want a string", i, e)
			}

			list[i] = s
		}

		return list, nil
	}

	return nil, fmt.Errorf("unsupported value of type %T", v)
}

func (fx *Fixture) domainInfos() []DomainInfo {
	infos := make([]DomainInfo, len(fx.Domains))
	for i, d := range fx.Domains {
		infos[i] = DomainInfo{
			Name:             d.Name,
			VirtualHostname:  d.VirtualHost,
			VirtualIPAddress: d.VirtualIP,
			ClientCertMode:   d.ClientCertMode,
			ClientCertCA:     d.ClientCertCA,
			SSLCertificate:   d.SSLCertificate,
			SSLPrivateKey:    d.SSLPrivateKey,
		}
	}

	return infos
}

func (fx *Fixture) serverInfos(service string) []ServerInfo {
	servers := fx.Servers[service]
	infos := make([]ServerInfo, len(servers))

	for i, s := range servers {
		infos[i] = ServerInfo{ID: s.ID, Hostname: s.Hostname, Services: []string{service}}
	}

	return infos
}

// literalHost parses host as an IP literal, bracketed or not; an offline render
// never uses the DNS.
func literalHost(_ context.Context, host string) net.IP {
	return net.ParseIP(strings.Trim(host, "[]"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
