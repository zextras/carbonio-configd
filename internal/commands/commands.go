// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package commands holds the LDAP-backed provisioning lookups the config
// manager reads (garpu, garpb, gamau and the typed server/global attributes)
// behind a small Command wrapper with status, output and error tracking.
package commands

import (
	"context"
	stderrors "errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// Configuration key constants
const (
	zimbraServiceHostname = "zimbraServiceHostname"
	errLDAPNotInitialized = "native LDAP client not initialized"
)

// provisioningLDAP is the subset of *internal/ldap.Client used by the
// LDAP-backed provisioning commands. An interface so tests can stub the
// LDAP round-trips; production passes the concrete client unchanged.
type provisioningLDAP interface {
	GetServerConfig(hostname string) (map[string]string, error)
	GetGlobalConfig() (map[string]string, error)
	GetAllServersWithAttributes() (map[string]map[string]string, error)
}

// CommandExecutor holds an LDAP client and provides methods for
// executing LDAP-dependent provisioning commands. This replaces
// the former package-level nativeLdapClient global variable.
type CommandExecutor struct {
	ldapClient provisioningLDAP
}

// NewCommandExecutor creates a new CommandExecutor with the given LDAP client.
// The client may be nil; LDAP-dependent commands will return an error in that case.
func NewCommandExecutor(client *ldap.Client) *CommandExecutor {
	if client == nil {
		// Keep the interface itself nil so `ldapClient == nil` guards work;
		// a typed-nil *ldap.Client inside the interface would defeat them.
		return &CommandExecutor{}
	}

	return &CommandExecutor{ldapClient: client}
}

// requireLDAP returns an error if the executor has no LDAP client configured.
func (e *CommandExecutor) requireLDAP() error {
	if e.ldapClient == nil {
		return stderrors.New(errLDAPNotInitialized)
	}

	return nil
}

// Command represents a command to be executed, mirroring jylibs/commands.py Command class.
type Command struct {
	Desc string                                                    // Description of the command
	Name string                                                    // Name of the command
	Func func(ctx context.Context, args ...string) (string, error) // Go function to execute
	Args []string
	// State tracking fields
	Status      int
	Output      string
	Error       string
	LastChecked time.Time
}

// NewCommand creates a new Command instance.
func NewCommand(
	desc, name string,
	fn func(ctx context.Context, args ...string) (string, error),
	cmdArgs ...string,
) *Command {
	c := &Command{
		Desc:   desc,
		Name:   name,
		Func:   fn,
		Args:   cmdArgs,
		Status: 0,
		Output: "",
		Error:  "",
	}
	c.resetState()

	return c
}

func (c *Command) resetState() {
	c.Status = 0
	c.Output = ""
	c.Error = ""
}

// DefaultCommandTimeout is the default timeout for command execution (60 seconds)
const DefaultCommandTimeout = 60 * time.Second

// Execute runs the command or function with a default 60-second timeout.
// For custom timeouts, use ExecuteWithContext.
func (c *Command) Execute(ctx context.Context, args ...string) (exitCode int, stdout string, stderr string) {
	ctx = logger.ContextWithComponent(ctx, "commands")

	ctx, cancel := context.WithTimeout(ctx, DefaultCommandTimeout)
	defer cancel()

	return c.ExecuteWithContext(ctx, args...)
}

// ExecuteWithContext runs the command or function with the provided context.
// The context can be used for cancellation, deadlines, or passing values.
func (c *Command) ExecuteWithContext(ctx context.Context, args ...string) (exitCode int, stdout string, stderr string) {
	ctx = logger.ContextWithComponent(ctx, "commands")
	logger.DebugContext(ctx, "Executing command",
		"command", c.String())
	c.resetState()
	c.LastChecked = time.Now()

	t1 := time.Now()

	var (
		output string
		err    error
		rc     int
	)

	if c.Func != nil {
		output, err = c.Func(ctx, args...)
		if err != nil {
			rc = 1 // Indicate error
		}
	}

	dt := time.Since(t1)

	c.Status = rc
	if err != nil {
		c.Error = fmt.Sprintf("UNKNOWN: %s died with error: %v", c.Name, err)
		logger.ErrorContext(ctx, "Command execution failed",
			"error", c.Error,
			"name", c.Name)
		// In Jython, this raises an exception, here we return the error status
		return c.Status, c.Output, c.Error
	}

	if output == "" {
		output = "UNKNOWN OUTPUT"
	}

	if rc == 0 {
		c.Error = "OK"
	} else {
		c.Error = "UNKNOWN ERROR"
	}

	c.Output = output

	if rc != 0 {
		logger.InfoContext(ctx, "Command executed with error",
			"command", c.String(),
			"return_code", rc,
			"output_len", len(output),
			"error_len", len(c.Error),
			"duration_sec", fmt.Sprintf("%.2f", dt.Seconds()),
			"output", output)
	} else {
		logger.DebugContext(ctx, "Command executed successfully",
			"command", c.String(),
			"return_code", rc,
			"output_len", len(output),
			"error_len", len(c.Error),
			"duration_sec", fmt.Sprintf("%.2f", dt.Seconds()))
	}

	return c.Status, c.Output, c.Error
}

func (c *Command) String() string {
	return fmt.Sprintf("%s(%v)", c.Name, redact.Args(c.Args))
}

// --- Dummy functions for provisioning commands (to be replaced with actual LDAP/API calls) ---

// ServerAttrs returns the server's LDAP attributes (with global defaults
// inherited, as zmprov gs does) for hostname. Multi-valued attributes are
// joined with newlines.
func (e *CommandExecutor) ServerAttrs(ctx context.Context, hostname string) (map[string]string, error) {
	if err := e.requireLDAP(); err != nil {
		return nil, err
	}

	logger.DebugContext(ctx, "Using native LDAP client for server query",
		"hostname", hostname)

	serverAttrs, err := e.ldapClient.GetServerConfig(hostname)
	if err != nil {
		return nil, fmt.Errorf("native LDAP query failed for server %s: %w", hostname, err)
	}

	logger.DebugContext(ctx, "Native LDAP query successful",
		"hostname", hostname,
		"attr_count", len(serverAttrs))

	return serverAttrs, nil
}

// GlobalAttrs returns the global configuration (cn=config,cn=zimbra) LDAP
// attributes. Multi-valued attributes are joined with newlines.
func (e *CommandExecutor) GlobalAttrs(ctx context.Context) (map[string]string, error) {
	if err := e.requireLDAP(); err != nil {
		return nil, err
	}

	logger.DebugContext(ctx, "Using native LDAP client for global config query")

	globalAttrs, err := e.ldapClient.GetGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("native LDAP query failed for global config: %w", err)
	}

	logger.DebugContext(ctx, "Native LDAP query successful",
		"attr_count", len(globalAttrs))

	return globalAttrs, nil
}

// sortedServers yields server attribute maps ordered by server name (cn).
// Go map iteration order is random; without a stable order the joined server
// lists change between cycles and trigger spurious rewrites and restarts.
// Legacy LdapProvisioning.getAllServers sorts by name as well.
func sortedServers(servers map[string]map[string]string) iter.Seq2[string, map[string]string] {
	return func(yield func(string, map[string]string) bool) {
		for _, name := range slices.Sorted(maps.Keys(servers)) {
			if !yield(name, servers[name]) {
				return
			}
		}
	}
}

// getAllServersWithAttribute is a generic helper function that queries all servers
// with a specific attribute set to TRUE and builds URLs using the provided format.
func (e *CommandExecutor) getAllServersWithAttribute(
	ctx context.Context, attributeKey, urlFormat, cmdName string,
) (string, error) {
	if err := e.requireLDAP(); err != nil {
		return "", err
	}

	logger.DebugContext(ctx, "Querying all servers with attribute",
		"attribute", attributeKey,
		"command", cmdName)

	// Get all servers with full attributes
	servers, err := e.ldapClient.GetAllServersWithAttributes()
	if err != nil {
		return "", fmt.Errorf("%s: failed to query servers: %w", cmdName, err)
	}

	var urls []string

	// Iterate through servers and check for the attribute
	for _, serverAttrs := range sortedServers(servers) {
		// Get hostname
		hostname, ok := serverAttrs[zimbraServiceHostname]
		if !ok || hostname == "" {
			continue
		}

		// Check if attribute is set to TRUE
		attrValue, ok := serverAttrs[attributeKey]
		if ok && strings.EqualFold(attrValue, "TRUE") {
			urls = append(urls, fmt.Sprintf(urlFormat, hostname))
		}
	}

	logger.DebugContext(ctx, "Server query completed",
		"attribute", attributeKey,
		"servers_found", len(urls))

	return strings.Join(urls, " "), nil
}

func (e *CommandExecutor) gamau(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Executing gamau (getAllMtaAuthURLs) command")

	if err := e.requireLDAP(); err != nil {
		return "", err
	}

	// Mirror jylibs/commands.py gamau: for every server flagged
	// zimbraMtaAuthTarget, build URLUtil.getMtaAuthURL(server) =
	// zimbra_admin_service_scheme ("https://") + host + ":" + zimbraMtaAuthPort
	// (default 7073) + AdminConstants.ADMIN_SERVICE_URI ("/service/admin/soap/").
	servers, err := e.ldapClient.GetAllServersWithAttributes()
	if err != nil {
		return "", fmt.Errorf("gamau: failed to query servers: %w", err)
	}

	var urls []string

	for _, attrs := range sortedServers(servers) {
		hostname := attrs[zimbraServiceHostname]
		if hostname == "" {
			continue
		}

		if !strings.EqualFold(attrs[attrZimbraMtaAuthTarget], "TRUE") {
			continue
		}

		port := attrs[attrZimbraMtaAuthPort]
		if port == "" {
			port = defaultMtaAuthPort
		}

		urls = append(urls, fmt.Sprintf("https://%s:%s/service/admin/soap/", hostname, port))
	}

	return strings.Join(urls, " "), nil
}

func (e *CommandExecutor) garpu(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Executing garpu (getAllReverseProxyURLs) command")
	// Mirror jylibs/commands.py garpu: REVERSE_PROXY_PROTO is empty, so the URL
	// is the bare "host:7072" + ExtensionDispatcherServlet.EXTENSION_PATH
	// ("/service/extension") + "/nginx-lookup" with NO scheme prefix.
	return e.getAllServersWithAttribute(ctx, attrZimbraReverseProxyLookupTarget,
		"%s:7072/service/extension/nginx-lookup", "garpu")
}

// buildBackendURL returns the reverse-proxy backend "host:port" for a server
// attribute map, mirroring jylibs/commands.py garpb. No scheme is prefixed
// (REVERSE_PROXY_PROTO is empty). The port is the plaintext zimbraMailPort
// (default 80) for http/mixed/both mail modes and the zimbraMailSSLPort
// (default 0) otherwise. Returns ("", false) when the server is not a reverse
// proxy lookup target or has no zimbraMailMode (Jython skips mode==None).
func buildBackendURL(attrs map[string]string) (string, bool) {
	hostname := attrs[zimbraServiceHostname]
	if hostname == "" {
		return "", false
	}

	if !strings.EqualFold(attrs[attrZimbraReverseProxyLookupTarget], "TRUE") {
		return "", false
	}

	mailMode, hasMail := attrs[attrZimbraMailMode]
	if !hasMail || mailMode == "" {
		return "", false
	}

	var port string

	switch strings.ToLower(mailMode) {
	case mailModeHTTP, "mixed", "both":
		port = attrs[attrZimbraMailPort]
		if port == "" {
			port = defaultMailPort
		}
	default:
		port = attrs[attrZimbraMailSSLPort]
		if port == "" {
			port = defaultMailSSLPort
		}
	}

	return fmt.Sprintf("%s:%s", hostname, port), true
}

// garpbEmptyFallback is the single backend emitted when no reverse-proxy backend
// qualifies, mirroring the Jython garpb hack (["    server localhost:8080;"]).
const garpbEmptyFallback = "    server localhost:8080;"

func (e *CommandExecutor) garpb(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Executing garpb (getAllReverseProxyBackends) command")

	if err := e.requireLDAP(); err != nil {
		return "", err
	}

	servers, err := e.ldapClient.GetAllServersWithAttributes()
	if err != nil {
		return "", fmt.Errorf("garpb: failed to query servers: %w", err)
	}

	var backends []string

	for _, serverAttrs := range sortedServers(servers) {
		if url, ok := buildBackendURL(serverAttrs); ok {
			backends = append(backends, url)
		}
	}

	logger.DebugContext(ctx, "Reverse proxy backends query completed",
		"backends_found", len(backends))

	if len(backends) == 0 {
		return garpbEmptyFallback, nil
	}

	return strings.Join(backends, " "), nil
}

// Registry holds the LDAP-backed lookups the config manager reads: the typed
// server/global attribute getters and the garpu/garpb/gamau misc commands. It
// is empty until RegisterLDAPCommands is called.
type Registry struct {
	Commands map[string]*Command
	// ServerAttrs and GlobalAttrs return typed LDAP attributes straight from
	// the native client; nil until RegisterLDAPCommands is called.
	ServerAttrs func(ctx context.Context, hostname string) (map[string]string, error)
	GlobalAttrs func(ctx context.Context) (map[string]string, error)
}

// NewRegistry creates an empty Registry. The registry is safe for concurrent
// use after RegisterLDAPCommands.
func NewRegistry() *Registry {
	return &Registry{Commands: make(map[string]*Command)}
}

// RegisterLDAPCommands registers LDAP-dependent commands using the given CommandExecutor.
// This must be called after the Registry is created and should be called by ConfigManager
// once it has created an LDAP client. It can be called multiple times to update the executor.
func (r *Registry) RegisterLDAPCommands(e *CommandExecutor) {
	if r.Commands == nil {
		r.Commands = make(map[string]*Command)
	}

	r.ServerAttrs = e.ServerAttrs
	r.GlobalAttrs = e.GlobalAttrs
	r.Commands[cmdGAMAU] = NewCommand("All MTA Authentication Target URLs", "getAllMtaAuthURLs", e.gamau)
	r.Commands[cmdGARPB] = NewCommand("All Reverse Proxy Backends", "getAllReverseProxyBackends", e.garpb)
	r.Commands[cmdGARPU] = NewCommand("All Reverse Proxy URLs", "getAllReverseProxyURLs", e.garpu)
}

// ResetProvisioning clears cached provisioning data for the specified type.
// This mirrors the Jython implementation's Provisioning.flushCache() behavior.
// In the Go implementation, we clear relevant in-memory caches rather than calling
// the Java Provisioning API.
//
// Types:
//   - "config": Global configuration cache
//   - "server": Server configuration cache
//   - "local": Local configuration cache (triggers reload)
func ResetProvisioning(ctx context.Context, pType string) {
	ctx = logger.ContextWithComponent(ctx, "commands")
	logger.DebugContext(ctx, "Resetting provisioning cache",
		"type", pType)

	// In the Go implementation, we don't have direct access to the Java Provisioning API
	// or a global cache instance here. The actual cache clearing is handled by the
	// ConfigManager in LoadAllConfigsWithRetry(), which:
	// 1. Calls this function for side effects/logging
	// 2. Clears State.FileCache
	// 3. Reloads configurations
	//
	// For future enhancement, if a global cache is added, this function should:
	// - Clear memory cache entries for the specified type
	// - Trigger reload for "local" type
	// - Flush LDAP connection pools if applicable

	switch pType {
	case "local":
		logger.DebugContext(ctx, "Local config cache reset - reload will be triggered")
	case "config":
		logger.DebugContext(ctx, "Global config cache reset")
	case "server":
		logger.DebugContext(ctx, "Server config cache reset")
	default:
		logger.WarnContext(ctx, "Unknown provisioning type for reset",
			"type", pType)
	}
}
