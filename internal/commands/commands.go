// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package commands provides a framework for executing external commands and internal functions.
// It supports both shell command execution and Go function calls with consistent error handling,
// logging, and output management. This package is used throughout configd for running
// system commands, provisioning functions, and configuration management operations.
package commands

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	errs "github.com/zextras/carbonio-configd/internal/errors"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/proxy"
)

// Configuration key constants
const (
	zimbraServiceHostname = "zimbraServiceHostname"
	errLDAPNotInitialized = "native LDAP client not initialized"
)

// CommandExecutor holds an LDAP client and provides methods for
// executing LDAP-dependent provisioning commands. This replaces
// the former package-level nativeLdapClient global variable.
type CommandExecutor struct {
	ldapClient *ldap.Client
}

// NewCommandExecutor creates a new CommandExecutor with the given LDAP client.
// The client may be nil; LDAP-dependent commands will return an error in that case.
func NewCommandExecutor(client *ldap.Client) *CommandExecutor {
	return &CommandExecutor{ldapClient: client}
}

// Command represents a command to be executed, mirroring jylibs/commands.py Command class.
type Command struct {
	Desc string // Description of the command
	Name string // Name of the command
	// Binary path for command execution (no shell interpolation)
	Binary  string                                                    // nolint: lll // Binary is intentionally short for alignment
	CmdArgs []string                                                  // Additional arguments prepended before runtime args
	Func    func(ctx context.Context, args ...string) (string, error) // Go function to execute
	Args    []string
	// State tracking fields
	Status      int
	Output      string
	Error       string
	LastChecked time.Time
	// Deprecated: Cmd field for format-string command execution (use Binary+CmdArgs instead)
	Cmd string
}

// NewCommand creates a new Command instance.
func NewCommand(
	desc, name, cmd string,
	fn func(ctx context.Context, args ...string) (string, error),
	cmdArgs ...string,
) *Command {
	c := &Command{
		Desc:    desc,
		Name:    name,
		Cmd:     cmd,
		Func:    fn,
		Args:    cmdArgs,
		Status:  0,
		Output:  "",
		Error:   "",
		CmdArgs: nil, // Initialize as empty slice
	}
	c.resetState()

	return c
}

func (c *Command) resetState() {
	c.Status = 0
	c.Output = ""
	c.Error = ""
}

// applyQuoteChar processes a quote rune for splitCommandArgs.
// Returns the updated inQuote/quoteChar state; appends an empty arg when an
// empty quoted segment is closed.
func applyQuoteChar(
	r, quoteChar rune,
	inQuote bool,
	current *strings.Builder,
	args *[]string,
) (newInQuote bool, newQuoteChar rune) {
	switch {
	case !inQuote:
		return true, r
	case r == quoteChar:
		if current.Len() == 0 {
			*args = append(*args, "")
		}

		return false, 0
	default:
		current.WriteRune(r)

		return inQuote, quoteChar
	}
}

// splitCommandArgs splits a command string into argv preserving quoted and
// escaped segments.
func splitCommandArgs(cmdStr string) ([]string, error) {
	var (
		args      []string
		current   strings.Builder
		inQuote   bool
		quoteChar rune
		escaped   bool
	)

	for _, r := range cmdStr {
		switch {
		case escaped:
			current.WriteRune(r)

			escaped = false
		case r == '\\':
			escaped = true
		case r == '"' || r == '\'':
			inQuote, quoteChar = applyQuoteChar(r, quoteChar, inQuote, &current, &args)
		case !inQuote && (r == ' ' || r == '\t'):
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if inQuote {
		return nil, fmt.Errorf("unterminated quote in command: %s", cmdStr)
	}

	if escaped {
		return nil, fmt.Errorf("trailing escape character in command: %s", cmdStr)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args, nil
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

	switch {
	case c.Binary != "":
		rc, output, err = c.runBinaryWithContext(ctx, args)
	case c.Cmd != "":
		rc, output, err = c.runCmdWithContext(ctx, c.formatCmd(args))
	case c.Func != nil:
		// Go function execution
		output, err = c.Func(ctx, args...)
		if err != nil {
			rc = 1 // Indicate error
		} else {
			rc = 0 // Indicate success
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

	if c.Error == "" {
		if rc == 0 {
			c.Error = "OK"
		} else {
			c.Error = "UNKNOWN ERROR"
		}
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

// formatCmd returns the command string with the first argument substituted
// when c.Cmd contains a "%s" placeholder. Without a placeholder the command
// is returned unchanged, regardless of how many args are provided.
//
// Deprecated: Use Binary+CmdArgs instead to avoid format-string construction.
func (c *Command) formatCmd(args []string) string {
	if len(args) == 0 {
		return c.Cmd
	}

	if !strings.Contains(c.Cmd, "%s") {
		return c.Cmd
	}

	return fmt.Sprintf(c.Cmd, args[0])
}

// runBinaryWithContext executes a command using the structured Binary+CmdArgs pattern.
// This avoids format-string interpolation entirely.
func (c *Command) runBinaryWithContext(ctx context.Context, args []string) (exitCode int, output string, err error) {
	cmdArgs := make([]string, 0, len(c.CmdArgs)+len(args))
	cmdArgs = append(cmdArgs, c.CmdArgs...)
	cmdArgs = append(cmdArgs, args...)

	logger.DebugContext(ctx, "Executing command",
		"binary", c.Binary, "args", cmdArgs)

	cmd := exec.CommandContext(ctx, c.Binary, cmdArgs...)
	outputBytes, cmdErr := cmd.CombinedOutput()
	output = string(outputBytes)

	if cmdErr != nil {
		var exitErr *exec.ExitError
		if stderrors.As(cmdErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}

		return exitCode, output, cmdErr
	}

	return 0, output, nil
}

func (c *Command) runCmdWithContext(ctx context.Context, cmdStr string) (exitCode int, output string, err error) {
	logger.DebugContext(ctx, "Executing shell command",
		"command", cmdStr)

	parts, parseErr := splitCommandArgs(cmdStr)
	if parseErr != nil {
		return 1, "", errs.WrapCommand("execute", cmdStr, 1, parseErr)
	}

	if len(parts) == 0 {
		return 1, "", errs.WrapCommand("execute", cmdStr, 1, fmt.Errorf(errs.ErrEmptyCommand))
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	outputBytes, cmdErr := cmd.CombinedOutput()
	output = string(outputBytes)
	err = cmdErr

	logger.DebugContext(ctx, "Shell command completed",
		"command", cmdStr,
		"return_code", cmd.ProcessState.ExitCode(),
		"output", output,
		"error", err)

	if err != nil {
		exitError := &exec.ExitError{}
		if stderrors.As(err, &exitError) {
			return exitError.ExitCode(), output, errs.WrapCommand("execute", cmdStr, exitError.ExitCode(), err)
		}

		return 1, output, errs.WrapCommand("execute", cmdStr, 1, err)
	}

	return 0, output, nil
}

func (c *Command) String() string {
	if c.Cmd != "" {
		return fmt.Sprintf("%s %s", c.Name, c.Cmd)
	}

	return fmt.Sprintf("%s %s(%v)", c.Name, c.Name, c.Args)
}

// --- Dummy functions for provisioning commands (to be replaced with actual LDAP/API calls) ---

func (e *CommandExecutor) getserver(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("hostname required for getserver")
	}

	hostname := args[0]

	// Use native LDAP client
	if e.ldapClient == nil {
		return "", stderrors.New(errLDAPNotInitialized)
	}

	logger.DebugContext(ctx, "Using native LDAP client for server query",
		"hostname", hostname)

	serverAttrs, err := e.ldapClient.GetServerConfig(hostname)
	if err != nil {
		return "", fmt.Errorf("native LDAP query failed for server %s: %w", hostname, err)
	}

	// Convert to zmprov format
	output := ldap.FormatAsZmprovOutput(serverAttrs)
	logger.DebugContext(ctx, "Native LDAP query successful",
		"hostname", hostname,
		"attr_count", len(serverAttrs))

	return output, nil
}

// getserverenabled retrieves enabled services for a server using the native LDAP client.
func (e *CommandExecutor) getserverenabled(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("hostname required for getserverenabled")
	}

	hostname := args[0]

	// Use native LDAP client
	if e.ldapClient == nil {
		return "", stderrors.New(errLDAPNotInitialized)
	}

	logger.DebugContext(ctx, "Using native LDAP client for server enabled services query",
		"hostname", hostname)

	serverAttrs, err := e.ldapClient.GetServerConfig(hostname)
	if err != nil {
		return "", fmt.Errorf("native LDAP query failed for server %s: %w", hostname, err)
	}

	// Extract only zimbraServiceEnabled attribute
	if services, ok := serverAttrs["zimbraServiceEnabled"]; ok {
		logger.DebugContext(ctx, "Native LDAP query successful - found enabled services",
			"hostname", hostname,
			"services", services)

		return "zimbraServiceEnabled: " + services + "\n", nil
	}

	logger.DebugContext(ctx, "No enabled services found for server",
		"hostname", hostname)

	return "", nil
}

func (e *CommandExecutor) getglobal(ctx context.Context, args ...string) (string, error) {
	// Use native LDAP client
	if e.ldapClient == nil {
		return "", stderrors.New(errLDAPNotInitialized)
	}

	logger.DebugContext(ctx, "Using native LDAP client for global config query")

	globalAttrs, err := e.ldapClient.GetGlobalConfig()
	if err != nil {
		return "", fmt.Errorf("native LDAP query failed for global config: %w", err)
	}

	// Convert to zmprov format
	output := ldap.FormatAsZmprovOutput(globalAttrs)
	logger.DebugContext(ctx, "Native LDAP query successful",
		"attr_count", len(globalAttrs))

	return output, nil
}

func getlocal(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Loading localconfig (XML + defaults + interpolation)")

	// Load configuration with defaults and ${variable} interpolation
	localCfg, err := localconfig.LoadResolvedConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load localconfig: %w", err)
	}

	// Convert to key=value format matching old zmlocalconfig -s output
	output := localconfig.FormatAsKeyValue(localCfg)

	logger.DebugContext(ctx, "Localconfig loaded successfully",
		"key_count", len(localCfg))

	return output, nil
}

// getAllServersWithAttribute is a generic helper function that queries all servers
// with a specific attribute set to TRUE and builds URLs using the provided format.
func (e *CommandExecutor) getAllServersWithAttribute(
	ctx context.Context, attributeKey, urlFormat, cmdName string,
) (string, error) {
	// Use native LDAP client
	if e.ldapClient == nil {
		return "", stderrors.New(errLDAPNotInitialized)
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
	for _, serverAttrs := range servers {
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
	// Query all servers with zimbraMtaAuthTarget=TRUE
	// Build MTA auth URLs (http://hostname:7025)
	return e.getAllServersWithAttribute(ctx, "zimbraMtaAuthTarget", "http://%s:7025", "gamau")
}

func (e *CommandExecutor) garpu(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Executing garpu (getAllReverseProxyURLs) command")
	// Query all mail client servers with zimbraReverseProxyLookupTarget=TRUE
	// Build reverse proxy URLs (https://hostname:7072/nginx-lookup)
	return e.getAllServersWithAttribute(ctx, "zimbraReverseProxyLookupTarget",
		"https://%s:7072/service/extension/nginx-lookup", "garpu")
}

// buildBackendURL returns the HTTPS backend URL for a server attribute map.
// Returns ("", false) when the server does not qualify as a reverse proxy backend.
func buildBackendURL(attrs map[string]string) (string, bool) {
	hostname := attrs[zimbraServiceHostname]
	if hostname == "" {
		return "", false
	}

	if !strings.EqualFold(attrs["zimbraReverseProxyLookupTarget"], "TRUE") {
		return "", false
	}

	mailMode, hasMail := attrs["zimbraMailMode"]
	if !hasMail || mailMode == "" {
		return "", false
	}

	httpsPort := attrs["zimbraMailSSLPort"]
	if httpsPort == "" {
		httpsPort = "443"
	}

	return fmt.Sprintf("https://%s:%s", hostname, httpsPort), true
}

func (e *CommandExecutor) garpb(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Executing garpb (getAllReverseProxyBackends) command")

	if e.ldapClient == nil {
		return "", stderrors.New(errLDAPNotInitialized)
	}

	servers, err := e.ldapClient.GetAllServersWithAttributes()
	if err != nil {
		return "", fmt.Errorf("garpb: failed to query servers: %w", err)
	}

	var backends []string

	for _, serverAttrs := range servers {
		if url, ok := buildBackendURL(serverAttrs); ok {
			backends = append(backends, url)
		}
	}

	logger.DebugContext(ctx, "Reverse proxy backends query completed",
		"backends_found", len(backends))

	return strings.Join(backends, " "), nil
}

// parseProxygenArgs parses the proxygen argument list and returns hostname, dryRun, verbose.
func parseProxygenArgs(args []string) (hostname string, dryRun, verbose bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-s":
			if i+1 < len(args) {
				hostname = args[i+1]
				i++
			}
		case "-d", "--dry-run":
			dryRun = true
		case "-v", "--verbose":
			verbose = true
		default:
			if hostname == "" && !strings.HasPrefix(args[i], "-") {
				hostname = args[i]
			}
		}
	}

	return hostname, dryRun, verbose
}

func proxygen(ctx context.Context, args ...string) (string, error) {
	logger.DebugContext(ctx, "Executing proxygen command",
		"args", args)

	hostname, dryRun, verbose := parseProxygenArgs(args)

	if hostname == "" {
		return "", fmt.Errorf("hostname required for proxygen (use -s hostname)")
	}

	logger.DebugContext(ctx, "Running Go proxy configuration generator",
		"hostname", hostname)

	// Determine base directory
	baseDir := os.Getenv("ZEXTRAS_HOME")
	if baseDir == "" {
		baseDir = "/opt/zextras"
	}

	// Create base configuration
	cfg := &config.Config{
		BaseDir:  baseDir,
		Hostname: hostname,
	}

	// Initialize LDAP client (nil for now - will be implemented later)
	var ldapClient *ldap.Ldap

	// Load configuration and create generator
	// Pass nil configs - LoadConfiguration will create empty ones for standalone mode
	// When called from ConfigManager, it should use RunProxygenWithConfigs instead
	gen, err := proxy.LoadConfiguration(ctx, cfg, nil, nil, nil, ldapClient, nil)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to initialize proxy generator",
			"error", err)

		return "", fmt.Errorf("proxy generator initialization failed: %w", err)
	}

	// Set dry-run and verbose modes if requested
	gen.SetDryRun(ctx, dryRun)
	gen.SetVerbose(ctx, verbose)

	// Generate all nginx configuration files
	logger.DebugContext(ctx, "Generating nginx proxy configuration files")

	if err := gen.GenerateAll(ctx); err != nil {
		logger.ErrorContext(ctx, "Proxy configuration generation failed",
			"error", err)

		return "", fmt.Errorf("proxy configuration generation failed: %w", err)
	}

	successMsg := "Proxy configuration generated successfully"
	if dryRun {
		successMsg += " (dry-run mode - no files written)"
	}

	if verbose {
		successMsg += " (verbose mode - detailed logging enabled)"
	}

	logger.DebugContext(ctx, successMsg,
		"dry_run", dryRun,
		"verbose", verbose)

	return successMsg, nil
}

// postconfExec executes postconf with proper argument handling.
// Args format: "key=value" (the caller should format as key=value without extra quotes)
func postconfExec(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("postconf requires arguments")
	}

	arg := args[0]

	postconfPath := strings.Fields(Exe["POSTCONF"])[0]
	cmd := exec.CommandContext(ctx, postconfPath, "-e", arg)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("postconf command failed: %w (output: %s)", err, string(output))
	}

	return string(output), nil
}

// Exe map mirrors the exe dictionary in jylibs/commands.py
var Exe map[string]string

// Commands map mirrors the commands dictionary in jylibs/commands.py
var Commands map[string]*Command

var (
	initOnce sync.Once
)

// Initialize sets up executable paths and commands based on ZEXTRAS_HOME environment variable.
// This function is safe to call multiple times (initialization happens only once).
// LDAP-dependent commands (gacf, gamau, garpb, garpu, gs, gs:enabled) are not
// registered here — call RegisterLDAPCommands with a CommandExecutor to register them.
func Initialize() {
	initOnce.Do(func() {
		baseDir := os.Getenv("ZEXTRAS_HOME")
		if baseDir == "" {
			baseDir = "/opt/zextras"
		}

		binDir := baseDir + "/bin"

		Exe = map[string]string{
			"AMAVIS":    binDir + "/zmamavisdctl",
			"ANTISPAM":  binDir + "/zmantispamctl",
			"ANTIVIRUS": binDir + "/zmclamdctl",
			"ARCHIVING": binDir + "/zmamavisdctl",
			"CBPOLICYD": binDir + "/zmcbpolicydctl",
			"LDAP":      binDir + "/ldap",
			"MAILBOX":   binDir + "/zmstorectl",
			"MAILBOXD":  binDir + "/zmmailboxdctl",
			"MEMCACHED": binDir + "/zmmemcachedctl",
			"MTA":       binDir + "/zmmtactl",
			"OPENDKIM":  baseDir + "/bin/zmopendkimctl", // Assuming also in bin
			"POSTCONF":  binDir + "/postconf -e",
			"POSTCONFD": binDir + "/postconf -X",
			"PROXY":     binDir + "/zmproxyctl",
			"SASL":      binDir + "/zmsaslauthdctl",
			"SERVICE":   binDir + "/zmmailboxdctl",
			"STATS":     binDir + "/zmstatctl",
		}

		Commands = map[string]*Command{
			"amavis":      {Desc: "amavis", Name: "amavis", Binary: Exe["AMAVIS"]},
			"antispam":    {Desc: "antispam", Name: "antispam", Binary: Exe["ANTISPAM"]},
			"antivirus":   {Desc: "antivirus", Name: "antivirus", Binary: Exe["ANTIVIRUS"]},
			"archiving":   {Desc: "archiving", Name: "archiving", Binary: Exe["ARCHIVING"]},
			"cbpolicyd":   {Desc: "cbpolicyd", Name: "cbpolicyd", Binary: Exe["CBPOLICYD"]},
			"ldap":        {Desc: "ldap", Name: "ldap", Binary: Exe["LDAP"]},
			"localconfig": {Desc: "Local server configuration", Name: "localconfig", Func: getlocal},
			"mailbox":     {Desc: "mailbox", Name: "mailbox", Binary: Exe["MAILBOX"]},
			"mailboxd":    {Desc: "mailboxd", Name: "mailboxd", Binary: Exe["MAILBOXD"]},
			"memcached":   {Desc: "memcached", Name: "memcached", Binary: Exe["MEMCACHED"]},
			"mta":         {Desc: "mta", Name: "mta", Binary: Exe["MTA"]},
			"opendkim":    {Desc: "opendkim", Name: "opendkim", Binary: Exe["OPENDKIM"]},
			"postconf":    {Desc: "postconf", Name: "postconf", Func: postconfExec},
			"postconfd":   {Desc: "postconfd", Name: "postconfd", Binary: Exe["POSTCONFD"]},
			"proxy":       {Desc: "proxy", Name: "proxy", Binary: Exe["PROXY"]},
			"proxygen":    {Desc: "proxygen", Name: "proxygen", Func: proxygen},
			"sasl":        {Desc: "sasl", Name: "sasl", Binary: Exe["SASL"]},
			"service":     {Desc: "service", Name: "service", Binary: Exe["SERVICE"]},
			"stats":       {Desc: "stats", Name: "stats", Binary: Exe["STATS"]},
		}
	})
}

// RegisterLDAPCommands registers LDAP-dependent commands using the given CommandExecutor.
// This must be called after Initialize() and should be called by ConfigManager once it
// has created an LDAP client. It can be called multiple times to update the executor.
func RegisterLDAPCommands(e *CommandExecutor) {
	if Commands == nil {
		Commands = make(map[string]*Command)
	}

	Commands["gacf"] = NewCommand("Global system configuration", "gacf", "", e.getglobal)
	Commands["gamau"] = NewCommand("All MTA Authentication Target URLs", "getAllMtaAuthURLs", "", e.gamau)
	Commands["garpb"] = NewCommand("All Reverse Proxy Backends", "getAllReverseProxyBackends", "", e.garpb)
	Commands["garpu"] = NewCommand("All Reverse Proxy URLs", "getAllReverseProxyURLs", "", e.garpu)
	Commands["gs"] = NewCommand("Configuration for server", "gs", "", e.getserver)
	Commands["gs:enabled"] = NewCommand("Enabled Services for host", "gs:enabled", "", e.getserverenabled)
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
