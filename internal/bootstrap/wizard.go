// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// ErrAborted is returned when the operator declines the summary.
var ErrAborted = errors.New("bootstrap configuration aborted")

// WizardMode selects which questions the wizard asks.
type WizardMode int

const (
	// AskUnset asks the questions whose key no file, environment variable
	// or flag set; defaults and detected values are offered pre-filled.
	AskUnset WizardMode = iota
	// AskAll asks every question, pre-filled with the current value.
	AskAll
)

// Resolver is the DNS the wizard checks names against.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// Wizard asks the bootstrap questions on a terminal.
type Wizard struct {
	In  *bufio.Reader
	Out io.Writer
	// ReadSecret reads a line without echo; nil reads a plain line from In.
	ReadSecret func() (string, error)
	// Resolver, if set, checks the hostname and the domain MX in DNS. A
	// failed check is a warning: the node may be set up before its DNS.
	Resolver Resolver
	// Host recomputes the defaults after each answer.
	Host Host
}

// question is one wizard prompt.
type question struct {
	path   string
	prompt string
	// when reports whether the question applies (nil: always).
	when func(c *Config) bool
}

var questions = []question{
	{keyHostHostname, "Hostname of this node", nil},
	{keyHostPublicHostname, "Public hostname users connect to", nil},
	{keyLdapHost, "LDAP master host", nil},
	{keyLdapPort, "LDAP master port", nil},
	{keyLdapReplicationType, "Directory server role (master, replica, mmr)", role(RoleDirectoryServer)},
	{keyLdapServerID, "Multi-master server ID", func(c *Config) bool { return c.LDAP.ReplicationType == ReplicationMMR }},
	{keyLdapRootPassword, "LDAP root password", role(RoleDirectoryServer)},
	{keyLdapAdminPassword, "LDAP admin (uid=zimbra) password", nil},
	{keyLdapReplicationPassword, "LDAP replication password", func(c *Config) bool {
		return c.HasRole(RoleDirectoryServer) && !c.IsLDAPMaster()
	}},
	{keyLdapPostfixPassword, "LDAP postfix password", func(c *Config) bool {
		return c.HasRole(RoleMTA) && !c.HasRole(RoleDirectoryServer)
	}},
	{keyLdapAmavisPassword, "LDAP amavis password", func(c *Config) bool {
		return c.HasRole(RoleMTA) && !c.HasRole(RoleDirectoryServer)
	}},
	{keyLdapNginxPassword, "LDAP nginx password", func(c *Config) bool {
		return c.HasRole(RoleProxy) && !c.HasRole(RoleDirectoryServer)
	}},
	{keyDomainCreate, "Create the default domain", role(RoleDirectoryServer)},
	{keyDomainName, "Default domain", func(c *Config) bool { return c.Domain.Create }},
	{keyAdminCreate, "Create the administrator account", role(RoleAppserver)},
	{keyAdminAccount, "Administrator account", func(c *Config) bool { return c.Admin.Create }},
	{keyAdminPassword, "Administrator password", func(c *Config) bool { return c.Admin.Create }},
	{keyMtaMyNetworks, "Trusted networks (comma-separated)", role(RoleMTA)},
	{keyStoreSMTPHost, "SMTP host the mailbox relays to", role(RoleAppserver)},
	{keyProxyMode, "Proxy mode (https, redirect)", role(RoleProxy)},
	{keyStartServices, "Start the services when done", nil},
}

// explicit reports whether src is a layer the operator chose.
func explicit(src Source) bool { return src >= SourceLegacy }

// recomputed reports whether Refresh recomputes a key of source src: the
// built-in defaults, never the values read from an existing install.
func recomputed(src Source) bool { return src != SourceUnset && src < SourceExisting }

// Run asks the questions, then shows the summary until the operator
// accepts it (or declines: ErrAborted).
func (w *Wizard) Run(ctx context.Context, r *Resolved, mode WizardMode) error {
	for {
		if err := w.ask(ctx, r, mode); err != nil {
			return err
		}

		out, err := r.Render(Print)
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(w.Out, "\nConfiguration:\n\n%s\n", out)

		ans, err := w.line("Proceed with this configuration? [Y]es, [e]dit, [n]o: ")
		if err != nil {
			return err
		}

		switch strings.ToLower(ans) {
		case "", "y", "yes":
			return nil
		case "e", "edit":
			mode = AskAll
		default:
			return ErrAborted
		}
	}
}

func (w *Wizard) ask(ctx context.Context, r *Resolved, mode WizardMode) error {
	for _, q := range questions {
		if err := r.Refresh(w.Host); err != nil {
			return err
		}

		if q.when != nil && !q.when(&r.Config) {
			continue
		}

		f := FieldByPath(q.path)
		if mode == AskUnset && explicit(r.Source(f.Path)) && r.Source(f.Path) != SourceWizard {
			continue
		}

		if err := w.askField(r, q, f); err != nil {
			return err
		}

		w.checkDNS(ctx, f.Path, &r.Config)
	}

	return r.Refresh(w.Host)
}

// askField asks one question until the answer is valid.
func (w *Wizard) askField(r *Resolved, q question, f *Field) error {
	for {
		var (
			ans string
			err error
		)

		if f.Secret {
			ans, err = w.secret(r, q, f)
		} else {
			ans, err = w.line(fmt.Sprintf("%s [%s]: ", q.prompt, f.Format(&r.Config)))
		}

		if err != nil {
			return err
		}

		if ans == "" {
			// Keep the current value; a required empty key is asked again.
			if !f.IsZero(&r.Config) || !IsRequired(f, &r.Config) {
				if f.Secret && !explicit(r.Source(f.Path)) {
					// Keep a generated secret: stop it being redrawn by
					// Refresh.
					r.mark(f, SourceWizard)
				}

				return nil
			}

			_, _ = fmt.Fprintf(w.Out, "  %s is required.\n", f.Path)

			continue
		}

		restore := r.snapshot(f)

		err = r.Set(f, ans, SourceWizard)
		if err == nil {
			err = CheckValue(f, &r.Config)
		}

		if err != nil {
			restore()

			_, _ = fmt.Fprintf(w.Out, "  %v\n", err)

			continue
		}

		return nil
	}
}

// secret reads a secret twice, without echo. Enter keeps the current value
// (or the generated one).
func (w *Wizard) secret(r *Resolved, q question, f *Field) (string, error) {
	hint := "required"

	switch {
	case r.Source(f.Path) == SourceGenerated:
		hint = "Enter: generate"
	case !f.IsZero(&r.Config):
		hint = "Enter: keep"
	}

	_, _ = fmt.Fprintf(w.Out, "%s [%s]: ", q.prompt, hint)

	first, err := w.readSecret()
	if err != nil || first == "" {
		return first, err
	}

	_, _ = fmt.Fprintf(w.Out, "Repeat %s: ", strings.ToLower(q.prompt[:1])+q.prompt[1:])

	second, err := w.readSecret()
	if err != nil {
		return "", err
	}

	if first != second {
		_, _ = fmt.Fprintln(w.Out, "  The two entries differ.")

		return w.secret(r, q, f)
	}

	return first, nil
}

func (w *Wizard) readSecret() (string, error) {
	if w.ReadSecret == nil {
		return w.readLine()
	}

	s, err := w.ReadSecret()
	_, _ = fmt.Fprintln(w.Out)

	return strings.TrimSpace(s), err
}

func (w *Wizard) line(prompt string) (string, error) {
	_, _ = fmt.Fprint(w.Out, prompt)

	return w.readLine()
}

func (w *Wizard) readLine() (string, error) {
	s, err := w.In.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("input ended before the configuration was complete")
		}

		return "", err
	}

	return strings.TrimSpace(s), nil
}

// checkDNS warns when the hostname does not resolve, resolves only to
// loopback addresses, or the domain has no MX record.
func (w *Wizard) checkDNS(ctx context.Context, path string, c *Config) {
	if w.Resolver == nil {
		return
	}

	switch path {
	case keyHostHostname:
		addrs, err := w.Resolver.LookupHost(ctx, c.Host.Hostname)
		if err != nil || len(addrs) == 0 {
			_, _ = fmt.Fprintf(w.Out, "  Warning: %s does not resolve in DNS.\n", c.Host.Hostname)

			return
		}

		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && !ip.IsLoopback() {
				return
			}
		}

		_, _ = fmt.Fprintf(w.Out, "  Warning: %s resolves only to loopback addresses.\n", c.Host.Hostname)
	case keyDomainName:
		if mx, err := w.Resolver.LookupMX(ctx, c.Domain.Name); err != nil || len(mx) == 0 {
			_, _ = fmt.Fprintf(w.Out, "  Warning: %s has no MX record in DNS.\n", c.Domain.Name)
		}
	}
}

// snapshot returns a function that puts f back as it is now: value, source
// and secret file.
func (r *Resolved) snapshot(f *Field) func() {
	val := reflectCopy(f.value(&r.Config))
	src, srcSet := r.sources[f.Path]
	file, fileSet := r.secretFiles[f.Path]

	return func() {
		f.value(&r.Config).Set(val)
		delete(r.sources, f.Path)
		delete(r.secretFiles, f.Path)

		if srcSet {
			r.sources[f.Path] = src
		}

		if fileSet {
			r.secretFiles[f.Path] = file
		}
	}
}

// Refresh recomputes every key no operator layer set, so the defaults follow
// the answers given so far (domain.name follows host.hostname).
func (r *Resolved) Refresh(host Host) error {
	for _, f := range Fields {
		if recomputed(r.sources[f.Path]) {
			f.value(&r.Config).SetZero()
			delete(r.sources, f.Path)
		}
	}

	return r.ApplyDefaults(host)
}
