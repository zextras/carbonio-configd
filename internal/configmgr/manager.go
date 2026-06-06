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
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

const (
	constTRUE                     = "TRUE"
	constFALSE                    = "FALSE"
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
	cachedLocalConfigOutput string             // Cached output from zmlocalconfig -s command
	cachedLocalConfigMu     sync.RWMutex       // Protects cachedLocalConfigOutput
	mtaExecutor             mtaops.Executor
	mtaResolver             mtaops.OperationResolver
	comparator              *Comparator
}

// NewConfigManager creates a new ConfigManager instance.
func NewConfigManager(ctx context.Context, mainCfg *config.Config,
	appState *state.State, ldapClient *ldap.Ldap, cacheInstance *cache.ConfigCache) *ConfigManager {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	cm := &ConfigManager{
		mainConfig:      mainCfg,
		State:           appState,
		LdapClient:      ldapClient,
		Cache:           cacheInstance,
		ServiceMgr:      services.NewServiceManager(), // Initialize service manager
		CommandRegistry: commands.NewRegistry(mainCfg.BaseDir),
	}
	cm.Transformer = transformer.NewTransformer(cm, appState)
	cm.mtaExecutor = mtaops.NewExecutor(mainCfg.BaseDir, ldapClient)
	cm.mtaResolver = mtaops.NewResolver(mainCfg.BaseDir)
	cm.initNativeLdapClient(ctx)
	cm.comparator = NewComparator(cm, appState, cm.ServiceMgr)

	return cm
}

// loadLocalConfigForLDAP is the localconfig loader used by initNativeLdapClient.
// Production points it at localconfig.LoadLocalConfig; tests override it to
// inject deterministic localconfig maps so the URL-validation branches can be
// exercised without touching disk.
var loadLocalConfigForLDAP = localconfig.LoadLocalConfig

// initNativeLdapClient initializes the native LDAP client from localconfig.
// If initialization fails, it logs a warning and the manager will fall back to zmprov subprocess calls.
func (cm *ConfigManager) initNativeLdapClient(ctx context.Context) {
	localCfg, err := loadLocalConfigForLDAP()
	if err != nil {
		logger.WarnContext(ctx, "Failed to load localconfig for native LDAP client - LDAP queries will fail",
			"error", err)

		return
	}

	// Extract LDAP connection parameters. ldap_url may be a single URL or a
	// space-separated list of URLs (Carbonio HA convention) — ParseURLs
	// normalises both forms (CO-3565).
	ldapURL, ok := localCfg["ldap_url"]
	if !ok || ldapURL == "" {
		logger.WarnContext(ctx, "LDAP URL not found in localconfig - LDAP queries will fail")

		return
	}

	ldapURLs := ldap.ParseURLs(ldapURL)
	if len(ldapURLs) == 0 {
		logger.WarnContext(ctx, "LDAP URL is empty after parsing - LDAP queries will fail",
			"ldap_url", ldapURL)

		return
	}

	bindDN, ok := localCfg["zimbra_ldap_userdn"]
	if !ok || bindDN == "" {
		logger.WarnContext(ctx, "LDAP user DN not found in localconfig - LDAP queries will fail")

		return
	}

	password, ok := localCfg["zimbra_ldap_password"]
	if !ok || password == "" {
		logger.WarnContext(ctx, "LDAP password not found in localconfig - LDAP queries will fail")

		return
	}

	nativeClient, err := ldap.NewClient(&ldap.ClientConfig{
		URLs:     ldapURLs,
		BindDN:   bindDN,
		Password: password,
		BaseDN:   "cn=zimbra",
		PoolSize: 5,
		StartTLS: cm.mainConfig.LdapStartTLSRequired,
	})
	if err != nil {
		logger.WarnContext(ctx, "Failed to create native LDAP client - LDAP queries will fail",
			"error", err)

		return
	}

	cm.NativeLdapClient = nativeClient

	logger.InfoContext(ctx, "Native LDAP client initialized successfully",
		"ldap_urls", ldapURLs,
		"bind_dn", bindDN)

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
// Failure here is asymmetric to the data client: the data client degrades to
// zmprov on failure, but the config client is the only correct write path, so
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

// lookupVarKey checks GlobalConfig → MiscConfig → ServerConfig for key.
func (cm *ConfigManager) lookupVarKey(key string) (string, bool) {
	if val, ok := cm.State.GlobalConfig.Data.Get(key); ok {
		return val, true
	}

	if val, ok := cm.State.MiscConfig.Data.Get(key); ok {
		return val, true
	}

	if val, ok := cm.State.ServerConfig.Data.Get(key); ok {
		return val, true
	}

	return "", false
}

// lookupFileKey returns the value for key from the file cache or disk.
func (cm *ConfigManager) lookupFileKey(ctx context.Context, key string) (string, error) {
	if val, ok := cm.State.FileCache[key]; ok {
		logger.DebugContext(ctx, "Loaded from cache", "key", key, "value", val)

		return val, nil
	}

	filePath := cm.mainConfig.BaseDir + "/conf/" + key

	//nolint:gosec // G304: File path comes from trusted configuration
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		logger.ErrorContext(ctx, "Error reading file", "file_path", filePath, "error", err)

		return "", fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	var filteredLines []string

	for line := range strings.SplitSeq(string(contentBytes), "\n") {
		if t := strings.TrimSpace(cm.Transformer.Transform(ctx, line)); t != "" {
			filteredLines = append(filteredLines, t)
		}
	}

	value := strings.Join(filteredLines, ", ")
	cm.State.FileCache[key] = value

	logger.DebugContext(ctx, "Loaded file content", "key", key, "value", value)

	return value, nil
}

// lookupMappedFileKey resolves a MAPFILE or MAPLOCAL key to its filesystem path.
func (cm *ConfigManager) lookupMappedFileKey(ctx context.Context, cfgType, key string) (string, error) {
	mappedPath, ok := state.MAPPEDFILES[key]
	if !ok {
		logger.WarnContext(ctx, "Key not in MAPPEDFILES", "config_type", cfgType, "key", key)

		return "", fmt.Errorf("key '%s' not in MAPPEDFILES", key)
	}

	fullPath := cm.mainConfig.BaseDir + "/" + mappedPath

	var value string

	if _, err := os.Stat(fullPath); err == nil {
		value = fullPath
	} else if !os.IsNotExist(err) {
		logger.ErrorContext(ctx, "Error stating file", "file_path", fullPath, "error", err)

		return "", fmt.Errorf("error stating file %s: %w", fullPath, err)
	}

	logger.DebugContext(ctx, "Mapped file lookup result",
		"config_type", cfgType, "key", key, "value", value)

	return value, nil
}

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
		value, found = cm.lookupVarKey(key)
	case configTypeLOCAL:
		value, found = cm.State.LocalConfig.Data.Get(key)
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
		if _, ok := cm.State.ServerConfig.ServiceConfig.Get(key); ok {
			value = constTRUE
		} else {
			value = constFALSE
		}

		found = true
	default:
		logger.WarnContext(ctx, "Unknown config type", "config_type", cfgType, "key", key)

		return "", fmt.Errorf("unknown config type %s", cfgType)
	}

	if !found {
		return "", fmt.Errorf("key %s not found in type %s", key, cfgType)
	}

	logger.DebugContext(ctx, "Config lookup completed",
		"key", key, "config_type", cfgType, "value", value)

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

// Comparator returns the embedded Comparator instance.
func (cm *ConfigManager) Comparator() *Comparator {
	return cm.comparator
}
