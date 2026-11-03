// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package ldapcmd implements "configd ldap": the zmprov verbs configd and its
// callers need (gacf, gcf, gs, mcf) plus a plain search, spoken straight to
// LDAP so no JVM and no mailbox are involved.
//
// Credentials come from the effective localconfig (defaults, localconfig.xml,
// CONFIGD_LC_* environment), which the --url, --bind-dn and --password-file
// flags override. The command does not require the zextras user: the
// credentials are explicit, and a caller such as avdb runs under another UID.
package ldapcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Verb is the operation of one run.
type Verb string

// The verbs of "configd ldap".
const (
	// VerbGacf prints the global config (zmprov gacf).
	VerbGacf Verb = "gacf"
	// VerbGcf prints chosen attributes of the global config (zmprov gcf).
	VerbGcf Verb = "gcf"
	// VerbGs prints a server entry with global inheritance (zmprov gs).
	VerbGs Verb = "gs"
	// VerbMcf modifies the global config (zmprov mcf).
	VerbMcf Verb = "mcf"
	// VerbSearch is a plain LDAP search.
	VerbSearch Verb = "search"
)

// Search scopes accepted by --scope.
const (
	ScopeBase = "base"
	ScopeOne  = "one"
	ScopeSub  = "sub"
)

// Localconfig keys the flags override (the fourth layer of the effective
// localconfig).
const (
	lcKeyURL       = "ldap_url"
	lcKeyMasterURL = "ldap_master_url"
	lcKeyBindDN    = "zimbra_ldap_userdn"
	lcKeyPassword  = "zimbra_ldap_password"
	lcKeyHostname  = "zimbra_server_hostname"
)

// Conn is the part of *ldap.Client the verbs use.
type Conn interface {
	GetGlobalConfig() (map[string]string, error)
	GetServerConfig(hostname string) (map[string]string, error)
	Search(baseDN, filter string, attributes []string, scope int) (*goldap.SearchResult, error)
	ModifyValues(dn string, add, del, replace map[string][]string) error
	Close() error
}

// Dial opens a Conn for the connection parameters.
type Dial func(*carboldap.ClientConfig) (Conn, error)

// Options are the parsed arguments of "configd ldap".
type Options struct {
	Verb Verb
	// Args are the positional arguments of the verb: attributes for gcf, the
	// optional server for gs, the [+|-]attr value pairs for mcf.
	Args []string

	// URLs, BindDN and PasswordFile override the effective localconfig. The
	// password is only ever read from a file, never from the command line.
	URLs         []string
	BindDN       string
	PasswordFile string
	// JSON selects the machine-readable output.
	JSON bool

	// Base, Filter, Scope and Attrs are the search parameters.
	Base   string
	Filter string
	Scope  string
	Attrs  []string

	// ConfigPath is the localconfig.xml to read; empty means the install's own.
	ConfigPath string
	// FS reads the password file and the attrs schema (default: the host's).
	FS sys.FS
	// Dial opens the LDAP connection (default: a pooled ldap.Client).
	Dial Dial
}

// Run executes the verb and returns the process exit status: 0 on success,
// 1 on any failure (reported on stderr).
func Run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	if err := execute(ctx, stdout, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

func execute(_ context.Context, stdout io.Writer, opts *Options) error {
	if err := validate(opts); err != nil {
		return err
	}

	fsys := opts.FS
	if fsys == nil {
		fsys = sys.RealFS{}
	}

	lc, err := effectiveConfig(fsys, opts)
	if err != nil {
		return err
	}

	params, err := carboldap.ParamsFrom(lc, carboldap.PurposeWrite)
	if errors.Is(err, carboldap.ErrNoURL) {
		return errors.New("no LDAP URL: pass --url or set CONFIGD_LC_ldap_url")
	}

	if err != nil {
		return err
	}

	// The command reaches whatever the URLs say; StartTLS follows what the
	// directory advertises (ldap_starttls_supported), not the write default.
	params.StartTLS = carboldap.StartTLSSupported(lc)
	params.FS = fsys

	dial := opts.Dial
	if dial == nil {
		dial = dialClient
	}

	conn, err := dial(params)
	if err != nil {
		return fmt.Errorf("connecting to LDAP: %w", err)
	}

	defer func() { _ = conn.Close() }()

	return dispatch(stdout, conn, lc, opts)
}

func dialClient(params *carboldap.ClientConfig) (Conn, error) {
	return carboldap.NewClient(params)
}

// validate rejects arguments that no connection can fix, before any I/O.
func validate(opts *Options) error {
	switch opts.Verb {
	case VerbGacf:
		return noArgs(opts)
	case VerbGcf:
		if len(opts.Args) == 0 {
			return errors.New("gcf needs at least one attribute")
		}
	case VerbGs:
		if len(opts.Args) > 1 {
			return errors.New("gs takes at most one server")
		}
	case VerbMcf:
		_, err := parseMcf(opts.Args)

		return err
	case VerbSearch:
		_, err := searchScope(opts.Scope)

		return err
	default:
		return fmt.Errorf("unknown verb %q", opts.Verb)
	}

	return nil
}

func noArgs(opts *Options) error {
	if len(opts.Args) > 0 {
		return fmt.Errorf("%s takes no arguments", opts.Verb)
	}

	return nil
}

// effectiveConfig loads the effective localconfig with the flags as its top
// layer.
func effectiveConfig(fsys sys.FS, opts *Options) (map[string]string, error) {
	overrides := make(map[string]string, 3)

	if len(opts.URLs) > 0 {
		urls := strings.Join(opts.URLs, " ")
		overrides[lcKeyURL] = urls
		overrides[lcKeyMasterURL] = urls
	}

	if opts.BindDN != "" {
		overrides[lcKeyBindDN] = opts.BindDN
	}

	if opts.PasswordFile != "" {
		data, err := fsys.ReadFile(opts.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("read password file: %w", err)
		}

		overrides[lcKeyPassword] = strings.TrimRight(string(data), "\r\n")
	}

	path := opts.ConfigPath
	if path == "" {
		path = cliutil.BasePath("conf", "localconfig.xml")
	}

	lc, err := localconfig.LoadEffectiveFrom(path, overrides)
	if err != nil {
		return nil, fmt.Errorf("loading localconfig: %w", err)
	}

	return lc, nil
}

func dispatch(stdout io.Writer, conn Conn, lc map[string]string, opts *Options) error {
	switch opts.Verb {
	case VerbGacf:
		return globalConfig(stdout, conn, nil, opts.JSON)
	case VerbGcf:
		return globalConfig(stdout, conn, opts.Args, opts.JSON)
	case VerbGs:
		return serverConfig(stdout, conn, lc, opts)
	case VerbMcf:
		return modifyConfig(conn, opts.Args)
	default:
		return search(stdout, conn, opts)
	}
}

func globalConfig(stdout io.Writer, conn Conn, only []string, asJSON bool) error {
	attrs, err := conn.GetGlobalConfig()
	if err != nil {
		return err
	}

	return printAttrs(stdout, pick(carboldap.NormalizeAttrs(attrs), only), asJSON)
}

func serverConfig(stdout io.Writer, conn Conn, lc map[string]string, opts *Options) error {
	server := lc[lcKeyHostname]
	if len(opts.Args) == 1 {
		server = opts.Args[0]
	}

	if server == "" {
		return errors.New("gs needs a server: pass one or set zimbra_server_hostname")
	}

	attrs, err := conn.GetServerConfig(server)
	if err != nil {
		return err
	}

	return printAttrs(stdout, carboldap.NormalizeAttrs(attrs), opts.JSON)
}
