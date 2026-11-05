// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package configmgr provides comprehensive configuration management for configd.
// It coordinates loading from multiple sources (LocalConfig, GlobalConfig, ServerConfig),
// handles change detection via MD5 fingerprinting, manages configuration rewrites,
// and orchestrates service restarts. The ConfigManager is the central component
// that ties together all configuration-related operations.
package configmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/redact"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

const (
	constTRUE                     = "TRUE"
	constFALSE                    = "FALSE"
	constYes                      = "yes"
	constNo                       = "no"
	constIPv4                     = "ipv4"
	constIPv6                     = "ipv6"
	zimbraServiceEnabled          = "zimbraServiceEnabled"
	localhostIPv4                 = "127.0.0.1"
	localhostIPv6                 = "::1"
	configTypeVAR                 = "VAR"      // Config lookup type for variable configs
	configTypeLOCAL               = "LOCAL"    // Config lookup type for local configs
	configTypeFILE                = "FILE"     // Config lookup type for file configs
	configTypeMAPFILE             = "MAPFILE"  // Config lookup type for mapped files
	configTypeMAPLOCAL            = "MAPLOCAL" // Config lookup type for mapped local files
	configTypeLITERAL             = "LITERAL"  // Literal value (no lookup)
	configTypeSERVICE             = "SERVICE"  // Config lookup type for service status
	componentProxy                = "proxy"
	serviceMTA                    = "mta"
	serviceMailbox                = "mailbox"
	sectionAmavis                 = "amavis"
	sectionDhparam                = "dhparam"
	sectionSasl                   = "sasl"
	sectionWebxml                 = "webxml"
	sectionNginx                  = "nginx"
	restrictionRejectRBLClient    = "reject_rbl_client"
	restrictionRejectRHSBLClient  = "reject_rhsbl_client"
	inetFamily                    = "inet"
	inet6Family                   = "inet6"
	loopbackIPv6                  = "[::1]"
	dataKeyMtaRestrictionRBLs     = "zimbraMtaRestrictionRBLs"
	dataKeyMtaRestrictionRHSBLCs  = "zimbraMtaRestrictionRHSBLCs"
	dataKeyMtaRestrictionRHSBLSs  = "zimbraMtaRestrictionRHSBLSs"
	dataKeyMtaRestrictionRHSBLRCs = "zimbraMtaRestrictionRHSBLRCs"
)

// ConfigManager manages all types of configurations.
type ConfigManager struct {
	mainConfig              *config.Config
	State                   *state.State // Reference to the central state object
	LdapClient              *ldap.Ldap
	NativeLdapClient        *ldap.Client       // Native LDAP client for direct queries (cn=zimbra, TCP)
	ConfigLdapClient        *ldap.Client       // Config LDAP client for cn=config writes (ldapi, rootDN)
	Cache                   *cache.ConfigCache // Configuration cache for LDAP and other data
	Transformer             *transformer.Transformer
	ServiceMgr              services.Manager   // Service manager interface
	CommandRegistry         *commands.Registry // Command registry for this instance
	cachedLocalConfigOutput string             // Cached resolved localconfig output
	cachedLocalConfigMu     sync.RWMutex       // Protects cachedLocalConfigOutput
	mtaExecutor             mtaops.Executor
	mtaResolver             mtaops.OperationResolver
	policy                  *restart.Policy
	metrics                 *metrics.Registry // operational counters; exported only when configured

	// The fields below belong to the (sequential) main loop: the plan of a
	// cycle is a function of prev (the snapshot the last plan was computed
	// from), tracked (the service tracking map) and carry (the actions the
	// last execution did not complete). deferred holds the restarts of
	// sections that changed during a REWRITE cycle until a cycle that is not
	// a REWRITE releases them (see holdDeferred). Like carry it is in memory:
	// a process restart forgets it, as it forgets the restarts of the plan.
	prev     *snapshot.Snapshot
	tracked  map[string]string
	carry    plan.Carry
	deferred []plan.Restart
	env      sys.System // host boundary: filesystem, process execution, clock

	// loadLocalConfig loads the effective localconfig: the LC.java defaults
	// under localconfig.xml under the CONFIGD_LC_* environment overlay,
	// interpolated once (legacy `zmlocalconfig -s -x` plus the overlay). The
	// `lc` loader and the LDAP client seams all read through it; tests inject
	// deterministic maps so no disk is needed.
	loadLocalConfig func() (map[string]string, error)
}

// NewConfigManager creates a new ConfigManager instance. Every host side effect
// of the manager and of the components it builds (MTA operations, commands,
// proxy generation) goes through env. serviceMgr is the one service manager of
// the process: restarts, the watchdog and status checks all share it.
func NewConfigManager(ctx context.Context, mainCfg *config.Config,
	appState *state.State, ldapClient *ldap.Ldap, cacheInstance *cache.ConfigCache,
	env sys.System, serviceMgr services.Manager) *ConfigManager {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	cm := &ConfigManager{
		mainConfig:      mainCfg,
		State:           appState,
		LdapClient:      ldapClient,
		Cache:           cacheInstance,
		ServiceMgr:      serviceMgr,
		CommandRegistry: commands.NewRegistry(),
		env:             env,

		loadLocalConfig: localconfig.LoadEffective,
	}
	cm.Transformer = transformer.NewTransformer(cm)
	cm.mtaExecutor = mtaops.NewExecutor(mainCfg.BaseDir, ldapClient, env)
	cm.mtaResolver = mtaops.NewResolver(mainCfg.BaseDir, env)
	cm.initNativeLdapClient(ctx)
	cm.metrics = metrics.New()
	cm.policy = restart.New(serviceMgr, env.Clock).WithMetrics(cm.metrics)

	// A manager that can drive systemd units (the host backend) gets the
	// systemd restart rules; a hook backend or a test double never does.
	if sd, ok := serviceMgr.(restart.Systemd); ok {
		cm.policy.WithSystemd(sd)
	}

	return cm
}

// Services returns the service manager that runs the config-driven restarts;
// the daemon shares it with the watchdog.
func (cm *ConfigManager) Services() *services.ServiceManager {
	sm, _ := cm.ServiceMgr.(*services.ServiceManager)

	return sm
}

// Metrics returns the registry the manager and the components it built record
// into (restarts, load failures); the main loop adds the cycle metrics and
// exports it.
func (cm *ConfigManager) Metrics() *metrics.Registry {
	return cm.metrics
}

// initNativeLdapClient initializes the native LDAP client from localconfig.
// If initialization fails, it logs a warning and the LDAP lookups fail until a later cycle can build the client.
func (cm *ConfigManager) initNativeLdapClient(ctx context.Context) {
	localCfg, err := cm.loadLocalConfig()
	if err != nil {
		logger.WarnContext(ctx, "Failed to load localconfig for native LDAP client - LDAP queries will fail",
			"error", err)

		return
	}

	// ldap_url may be a single URL or a space-separated list of URLs
	// (Carbonio HA convention): ParamsFrom normalises both forms (CO-3565).
	// Transport security comes from the localconfig just loaded (the legacy
	// Java rule, see ldap.ResolveTLS), not from mainConfig, which is only
	// populated later by LoadAllConfigs.
	params, err := ldap.ParamsFrom(ctx, localCfg, ldap.PurposeRead, cm.env.FS)
	if err != nil {
		logger.WarnContext(ctx, "LDAP connection parameters missing in localconfig - LDAP queries will fail",
			"error", err)

		return
	}

	params.Context = ctx

	nativeClient, err := ldap.NewClient(params)
	if err != nil {
		logger.WarnContext(ctx, "Failed to create native LDAP client - LDAP queries will fail",
			"error", err)

		return
	}

	cm.NativeLdapClient = nativeClient

	logger.InfoContext(ctx, "Native LDAP client initialized successfully",
		"ldap_urls", params.URLs,
		"bind_dn", params.BindDN,
		"starttls", params.StartTLS)

	executor := commands.NewCommandExecutor(nativeClient)
	cm.CommandRegistry.RegisterLDAPCommands(executor)

	if cm.LdapClient != nil {
		cm.LdapClient.SetNativeClient(ctx, nativeClient)
	}

	cm.initConfigLdapClient(ctx, localCfg)
}

// initConfigLdapClient builds the slapd-config (cn=config) write client and
// wires it into the Ldap wrapper. Config-backend writes (every keymap entry)
// require binding as the cn=config rootDN over the local ldapi socket; the
// data-suffix client (uid=zimbra) cannot write cn=config (LDAP code 50).
//
// Failure here is asymmetric to the data client: the data client logs a
// warning on failure, but the config client is the only correct write path, so
// a missing root password is logged at error level and writes will fail fast
// at call time (ModifyAttribute returns a config error) rather than silently
// using the wrong identity.
func (cm *ConfigManager) initConfigLdapClient(ctx context.Context, localCfg map[string]string) {
	if cm.LdapClient == nil {
		return
	}

	rootPassword, ok := localCfg["ldap_root_password"]
	if !ok || rootPassword == "" {
		logger.ErrorContext(ctx, "ldap_root_password not found in localconfig - cn=config writes will fail")

		return
	}

	configClient, err := ldap.NewClient(&ldap.ClientConfig{
		URL:      ldap.ConfigBackendURL,
		BindDN:   ldap.ConfigBackendDN,
		Password: rootPassword,
		BaseDN:   ldap.ConfigBackendDN,
		PoolSize: 1, // serialized low-volume reconcile writes
		StartTLS: false,
		FS:       cm.env.FS,
		Context:  ctx,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create config LDAP client - cn=config writes will fail",
			"error", err)

		return
	}

	cm.ConfigLdapClient = configClient
	cm.LdapClient.SetConfigClient(ctx, configClient)

	logger.InfoContext(ctx, "Config LDAP client initialized successfully",
		"ldap_url", ldap.ConfigBackendURL,
		"bind_dn", ldap.ConfigBackendDN)
}

// lookupFileKey returns the value for key from the file cache or disk.
func (cm *ConfigManager) lookupFileKey(ctx context.Context, key string) (string, error) {
	if val, ok := cm.lookupCachedFile(ctx, key); ok {
		if logger.IsDebug(ctx) {
			logger.DebugContext(ctx, "Loaded from cache", "key", key, "value", redact.ForLog(key, val))
		}

		return val, nil
	}

	filePath := cm.mainConfig.TemplatePath("conf/" + key)

	//nolint:gosec // G304: File path comes from trusted configuration
	contentBytes, err := cm.env.FS.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	var filteredLines []string

	for line := range strings.SplitSeq(string(contentBytes), "\n") {
		// Legacy lookUpConfig has no try/except around transform(): a raise
		// propagates to the caller before the value is cached.
		transformed, err := cm.Transformer.Transform(ctx, line)
		if err != nil {
			return "", fmt.Errorf("transform %s: %w", filePath, err)
		}

		if t := strings.TrimSpace(transformed); t != "" {
			filteredLines = append(filteredLines, t)
		}
	}

	value := strings.Join(filteredLines, ", ")
	cm.storeCachedFile(ctx, key, value)

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Loaded file content", "key", key, "value", redact.ForLog(key, value))
	}

	return value, nil
}

// lookupMappedFileKey resolves a MAPFILE or MAPLOCAL key to its filesystem path.
func (cm *ConfigManager) lookupMappedFileKey(ctx context.Context, cfgType, key string) (string, error) {
	mappedPath, ok := state.MAPPEDFILES[key]
	if !ok {
		logger.WarnContext(ctx, "Key not in MAPPEDFILES", "config_type", cfgType, "key", key)

		return "", fmt.Errorf("key '%s' not in MAPPEDFILES: %w", key, errLookupNotFound)
	}

	fullPath := cm.mainConfig.BaseDir + "/" + mappedPath

	var value string

	if _, err := cm.env.FS.Stat(fullPath); err == nil {
		value = fullPath
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("error stating file %s: %w", fullPath, err)
	}

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Mapped file lookup result",
			"config_type", cfgType, "key", key, "value", redact.ForLog(key, value))
	}

	return value, nil
}

// errLookupNotFound marks a lookup whose key is simply not defined, as opposed
// to a lookup that could not be performed (I/O or transform failure).
var errLookupNotFound = errors.New("key not found")

// lookupMissError is the miss of LookUpConfig: the message is only formatted if
// someone reads it (misses are routine for optional keys), and errors.Is
// matches errLookupNotFound.
type lookupMissError struct{ cfgType, key string }

func (e *lookupMissError) Error() string {
	return "key " + e.key + " not found in type " + e.cfgType + ": " + errLookupNotFound.Error()
}

func (e *lookupMissError) Is(target error) bool { return target == errLookupNotFound }

// LookUpConfig retrieves a configuration value based on its type and key.
// This method implements the lookup.ConfigLookup interface.
func (cm *ConfigManager) LookUpConfig(ctx context.Context, cfgType, key string) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Looking up config key", "key", key, "config_type", cfgType)

	var (
		value string
		found bool
		err   error
	)

	switch cfgType {
	case configTypeVAR:
		// Server overrides misc overrides global, matching the legacy
		// configd (state.py assigns global, misc, server; the last one wins).
		value, found = cm.State.Snapshot().Var(key)
	case configTypeLOCAL:
		value, found = cm.State.Snapshot().Local(key)
	case configTypeFILE:
		value, err = cm.lookupFileKey(ctx, key)
		if err != nil {
			return "", err
		}

		found = true
	case configTypeMAPFILE, configTypeMAPLOCAL:
		value, err = cm.lookupMappedFileKey(ctx, cfgType, key)
		if err != nil {
			return "", err
		}

		found = true
	case configTypeSERVICE:
		value, found = cm.State.Snapshot().Value(configTypeSERVICE, key)
	default:
		logger.WarnContext(ctx, "Unknown config type", "config_type", cfgType, "key", key)

		return "", fmt.Errorf("unknown config type %s", cfgType)
	}

	if !found {
		return "", &lookupMissError{cfgType: cfgType, key: key}
	}

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Config lookup completed",
			"key", key, "config_type", cfgType, "value", redact.ForLog(key, value))
	}

	return value, nil
}

// InvalidateLDAPCache invalidates all LDAP-related cache entries.
// This should be called when:
// - LDAP configuration changes are detected
// - Manual proxy configuration generation is requested
// - Config reload is triggered
func (cm *ConfigManager) InvalidateLDAPCache(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	if cm.Cache == nil {
		return
	}

	logger.DebugContext(ctx, "Invalidating LDAP cache")

	invalidatedCount := cm.Cache.InvalidateCacheByPrefix("ldap:")

	if invalidatedCount == 0 {
		logger.DebugContext(ctx, "No LDAP cache entries to invalidate")
	}
}

// ClearLocalConfigCache clears the cached localconfig output.
// This should be called when:
// - Manual reload is triggered (SIGHUP or network command)
// - Config reload is requested
// - System configuration may have changed
func (cm *ConfigManager) ClearLocalConfigCache(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	cm.cachedLocalConfigMu.Lock()
	defer cm.cachedLocalConfigMu.Unlock()

	if cm.cachedLocalConfigOutput != "" {
		logger.DebugContext(ctx, "Clearing localconfig cache")

		cm.cachedLocalConfigOutput = ""
	}
}
