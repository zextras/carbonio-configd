// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package ldap provides LDAP client functionality for querying and modifying
// Carbonio LDAP attributes. It handles cn=config modifications, domain queries,
// server queries, and implements retry logic for transient failures.
package ldap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	errs "github.com/zextras/carbonio-configd/internal/errors"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// AttributeModifier is the narrow LDAP write surface that mtaops needs.
// It is satisfied by *Ldap, which applies keymap resolution, attribute
// transforms, and retry semantics. Kept as an interface so callers (notably
// mtaops) can inject test doubles without a live LDAP connection.
type AttributeModifier interface {
	// ModifyAttribute modifies an LDAP attribute identified by an internal keymap key.
	ModifyAttribute(ctx context.Context, key, value string) error
}

// Ldap represents the LDAP client.
type Ldap struct {
	config         *config.Config
	pendingChanges map[string]string // Key -> Value
	// In a real implementation, this would hold an actual LDAP client connection.
	// For now, we'll simulate the behavior.
	IsMaster bool

	// Retry configuration
	MaxRetries    int           // Maximum number of retry attempts (default: 3)
	RetryDelay    time.Duration // Initial retry delay (default: 100ms)
	MaxRetryDelay time.Duration // Maximum retry delay (default: 5s)

	// Native LDAP client for direct LDAP queries against the cn=zimbra data
	// suffix (bound as uid=zimbra over TCP). Used for reads only.
	NativeClient *Client

	// ConfigClient writes the slapd-config (cn=config) backend, bound as the
	// cn=config rootDN over the local ldapi socket. All keymap entries target
	// cn=config, so this owns 100% of write traffic. The data-suffix
	// NativeClient cannot write cn=config (LDAP code 50), so writes must never
	// fall back to it.
	ConfigClient *Client
}

// LdapKeyMapEntry represents an entry in the LDAP key map.
//
//nolint:revive // LdapKeyMapEntry name is kept for backward compatibility
type LdapKeyMapEntry struct {
	Attr           string
	DN             string
	RequiresMaster bool
	TransformFmt   string
}

// LDAP DN constants used in keymap and lookupKey.
const (
	ldapCnConfig       = "cn=config"
	ldapDB3MdbCnConfig = "olcDatabase={3}mdb,cn=config"
	ldapDB2MdbCnConfig = "olcDatabase={2}mdb,cn=config"
)

// keymap mirrors the keymap in jylibs/ldap.py
//
//nolint:lll
var keymap = map[string]LdapKeyMapEntry{
	keyLDAPCommonLoglevel:        {"olcLogLevel", ldapCnConfig, false, "%s"},
	keyLDAPCommonThreads:         {"olcThreads", ldapCnConfig, false, "%s"},
	keyLDAPCommonToolthreads:     {"olcToolThreads", ldapCnConfig, false, "%s"},
	keyLDAPCommonRequireTLS:      {"olcSecurity", ldapCnConfig, false, "ssf=%s"},
	"ldap_common_writetimeout":   {"olcWriteTimeout", ldapCnConfig, false, "%s"},
	"ldap_common_tlsdhparamfile": {"olcTLSDHParamFile", ldapCnConfig, false, "%s"},
	"ldap_common_tlsprotocolmin": {"olcTLSProtocolMin", ldapCnConfig, false, "%s"},
	"ldap_common_tlsciphersuite": {"olcTLSCipherSuite", ldapCnConfig, false, "%s"},

	keyLDAPDBMaxsize:   {olcDBMaxsizeAttr, ldapDB3MdbCnConfig, false, "%s"},
	keyLDAPDBEnvflags:  {"olcDbEnvFlags", ldapDB3MdbCnConfig, false, "%s"},
	"ldap_db_rtxnsize": {"olcDbRtxnSize", ldapDB3MdbCnConfig, false, "%s"},

	keyLDAPAccesslogMaxsize:            {olcDBMaxsizeAttr, ldapDB2MdbCnConfig, true, "%s"},
	"ldap_accesslog_envflags":          {"olcDbEnvFlags", ldapDB2MdbCnConfig, true, "%s"},
	keyLDAPOverlaySyncprovCheckpoint:   {"olcSpCheckpoint", ldapOverlaySyncprovDB3DN, true, "%s"},
	"ldap_overlay_syncprov_sessionlog": {"olcSpSessionlog", ldapOverlaySyncprovDB3DN, true, "%s"},

	"ldap_overlay_accesslog_logpurge": {"olcAccessLogPurge", "olcOverlay={1}accesslog,olcDatabase={3}mdb,cn=config", true, "%s"},
}

// NewLdap initializes a new Ldap client with default retry configuration.
func NewLdap(ctx context.Context, cfg *config.Config) *Ldap {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	l := &Ldap{
		config:         cfg,
		pendingChanges: make(map[string]string),
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
		MaxRetryDelay:  5 * time.Second,
	}
	// In a real scenario, this would establish an LDAP connection.
	// For now, we'll assume it's successful.

	logger.DebugContext(ctx, "LDAP client initialized with retry config",
		"max_retries", l.MaxRetries,
		"retry_delay", l.RetryDelay,
		"max_retry_delay", l.MaxRetryDelay)

	return l
}

// SetNativeClient sets the native LDAP client for direct LDAP queries.
// This should be called by ConfigManager after initializing the native client.
func (l *Ldap) SetNativeClient(ctx context.Context, client *Client) {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	l.NativeClient = client

	if client != nil {
		logger.DebugContext(ctx, "Native LDAP client set for Ldap manager")
	} else {
		logger.DebugContext(ctx, "Native LDAP client cleared")
	}
}

// SetConfigClient sets the slapd-config (cn=config) write client, bound as the
// cn=config rootDN over the local ldapi socket. ConfigManager calls this after
// initializing the config client. All keymap writes are routed here.
func (l *Ldap) SetConfigClient(ctx context.Context, client *Client) {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	l.ConfigClient = client

	if client != nil {
		logger.DebugContext(ctx, "Config LDAP client set for Ldap manager")
	} else {
		logger.DebugContext(ctx, "Config LDAP client cleared")
	}
}

// AddChange adds an LDAP change to the pending queue.
func (l *Ldap) AddChange(ctx context.Context, key, value string) {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	logger.DebugContext(ctx, "Adding LDAP change",
		"key", key,
		"value", value)
	l.pendingChanges[key] = value
}

// GetPendingChanges returns the current pending LDAP changes.
func (l *Ldap) GetPendingChanges() map[string]string {
	return l.pendingChanges
}

// ClearPending clears all pending changes.
func (l *Ldap) ClearPending() {
	l.pendingChanges = make(map[string]string)
}

// ModifyAttribute modifies an LDAP attribute using the native LDAP client with retry logic.
// It resolves the internal key to its DN/attribute via the keymap, applies the
// configured transform format, and issues a real LDAP modify through NativeClient.
func (l *Ldap) ModifyAttribute(ctx context.Context, key, value string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")
	logger.InfoContext(ctx, "Setting LDAP attribute",
		"key", key,
		"value", value)

	l.refreshMasterStatus(ctx)

	// Validation happens outside retry logic (not retryable)
	entry, err := l.lookupKey(ctx, key)
	if err != nil {
		logger.ErrorContext(ctx, "LDAP lookup error",
			"error", err,
			"key", key)

		return err
	}

	val := fmt.Sprintf(entry.TransformFmt, value)

	// Writes target the slapd-config (cn=config) backend, which is only
	// writable by the cn=config rootDN over ldapi. Never fall back to the
	// data-suffix NativeClient (uid=zimbra) — it has no cn=config access and
	// would fail every attempt with LDAP code 50.
	if l.ConfigClient == nil {
		return fmt.Errorf("cannot modify LDAP attribute %q: config client not initialized", key)
	}

	// Execute LDAP modification with retry logic.
	return l.withRetry(ctx, fmt.Sprintf("modify %s=%s", key, value), func() error {
		return l.applyConfigModify(ctx, entry.DN, entry.Attr, val)
	})
}

// refreshMasterStatus mirrors jylibs/ldap.py modify_attribute: when the local
// node is configured as an LDAP master, probe cn=accesslog to confirm the
// accesslog database is actually present. A configured-but-unreachable master
// must not attempt master-only modifications, so the probe result (not the raw
// config flag) drives IsMaster. With no config client to probe, the config flag
// is trusted as a last resort.
func (l *Ldap) refreshMasterStatus(ctx context.Context) {
	if !l.config.LdapIsMaster {
		return
	}

	if l.ConfigClient == nil {
		l.IsMaster = true

		return
	}

	if l.ConfigClient.ProbeDN(ldapAccesslogSuffix) {
		l.IsMaster = true

		logger.DebugContext(ctx, "LDAP config is master")
	} else {
		l.IsMaster = false

		logger.DebugContext(ctx, "LDAP master probe failed; treating node as non-master")
	}
}

// applyConfigModify reads the current attribute value and replaces it only when
// it differs, mirroring the change-detection in jylibs/ldap.py:98. The
// olcSpSessionlog overlay attribute is skipped when absent because slapd rejects
// a replace of a non-existent sessionlog directive (ldap.py:99-100). Must be
// called with a non-nil ConfigClient.
func (l *Ldap) applyConfigModify(ctx context.Context, dn, attr, val string) error {
	origValue, present, err := l.ConfigClient.ReadAttribute(dn, attr)
	if err != nil {
		// On a read failure, fall back to the legacy presence semantics: skip
		// olcSpSessionlog (treated as absent) and otherwise attempt the replace.
		if attr == attrOlcSpSessionlog {
			logger.InfoContext(ctx, "olcSpSessionlog attribute is not present, skipping replace",
				"dn", dn)

			return nil
		}

		logger.DebugContext(ctx, "Could not read config attribute, attempting replace",
			"dn", dn,
			"attr", attr,
			"error", err)

		return l.ConfigClient.ModifyAttribute(dn, attr, val)
	}

	if origValue == val {
		logger.DebugContext(ctx, "LDAP attribute unchanged, skipping",
			"dn", dn,
			"attr", attr,
			"value", val)

		return nil
	}

	if attr == attrOlcSpSessionlog && !present {
		logger.InfoContext(ctx, "olcSpSessionlog attribute is not present, skipping replace",
			"dn", dn)

		return nil
	}

	logger.DebugContext(ctx, "Modifying LDAP attribute",
		"dn", dn,
		"attr", attr,
		"value", val)

	return l.ConfigClient.ModifyAttribute(dn, attr, val)
}

// lookupKey mirrors the lookupKey method in jylibs/ldap.py.
func (l *Ldap) lookupKey(ctx context.Context, key string) (LdapKeyMapEntry, error) {
	entry, ok := keymap[key]
	if !ok {
		return LdapKeyMapEntry{}, errs.NewConfigError("lookup", key)
	}

	// Adjust DN for ldap_db_ keys if not master, mirroring Jython behavior
	if strings.HasPrefix(key, "ldap_db_") && !l.IsMaster {
		entry.DN = ldapDB2MdbCnConfig
	}

	if entry.RequiresMaster && !l.IsMaster {
		logger.DebugContext(ctx, "LDAP: Trying to modify key when not a master",
			"key", key)

		return LdapKeyMapEntry{}, errs.WrapConfig("modify", key, fmt.Errorf(errs.ErrNotMaster))
	}

	logger.DebugContext(ctx, "Found key and dn",
		"attr", entry.Attr,
		"dn", entry.DN,
		"key", key,
		"is_master", l.IsMaster)

	return entry, nil
}

// ModifyAttributeBatch modifies multiple LDAP attributes in batches grouped by DN.
// This improves efficiency by combining multiple attribute modifications for the same DN
// into a single LDAP modify operation.
func (l *Ldap) ModifyAttributeBatch(ctx context.Context, changes map[string]string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "ldap")

	if len(changes) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Batch modifying LDAP attributes",
		"count", len(changes))

	l.refreshMasterStatus(ctx)

	// A missing config client is a configuration error, not a transient
	// failure: fail fast before entering the retry loop (which would
	// otherwise sleep/backoff per DN for an unrecoverable condition). Writes
	// must use the cn=config client (ldapi), never the data-suffix client.
	if l.ConfigClient == nil {
		return fmt.Errorf("cannot batch modify LDAP attributes: config client not initialized")
	}

	// Group changes by DN
	dnGroups := make(map[string]map[string]string) // DN -> map[Attr]Value

	for key, value := range changes {
		entry, err := l.lookupKey(ctx, key)
		if err != nil {
			logger.ErrorContext(ctx, "LDAP batch lookup error",
				"key", key,
				"error", err)

			return err
		}

		val := fmt.Sprintf(entry.TransformFmt, value)

		// Initialize DN group if needed
		if dnGroups[entry.DN] == nil {
			dnGroups[entry.DN] = make(map[string]string)
		}

		// Add attribute to DN group
		dnGroups[entry.DN][entry.Attr] = val
	}

	// Execute batch modifications for each DN with retry logic
	for dn, attrs := range dnGroups {
		err := l.withRetry(ctx, fmt.Sprintf("batch modify DN %s", dn), func() error {
			return l.executeBatchModifyInternal(ctx, dn, attrs)
		})
		if err != nil {
			logger.ErrorContext(ctx, "Failed to batch modify DN",
				"dn", dn,
				"error", err)

			return err
		}
	}

	return nil
}

// executeBatchModifyInternal performs the actual LDAP batch modification without retry logic.
// This is called by ModifyAttributeBatch through the withRetry wrapper. Each attribute on
// the DN is applied via the native client; the go-ldap library does not expose a
// transactional multi-attribute modify here, so attributes are replaced sequentially and
// the first failure aborts the remaining attributes for that DN.
func (l *Ldap) executeBatchModifyInternal(ctx context.Context, dn string, attrs map[string]string) error {
	logger.DebugContext(ctx, "Batch modifying DN",
		"dn", dn,
		"attribute_count", len(attrs))

	if l.ConfigClient == nil {
		return fmt.Errorf("cannot batch modify DN %q: config client not initialized", dn)
	}

	for attr, val := range attrs {
		if err := l.applyConfigModify(ctx, dn, attr, val); err != nil {
			return fmt.Errorf("modify %s on %s: %w", attr, dn, err)
		}
	}

	return nil
}

// withRetry executes an LDAP operation with exponential backoff retry logic.
// This handles transient failures such as connection timeouts, temporary unavailability,
// or network issues. Non-retryable errors (validation, permission) are returned immediately.
func (l *Ldap) withRetry(ctx context.Context, operation string, fn func() error) error {
	var lastErr error

	delay := l.RetryDelay

	for attempt := 0; attempt <= l.MaxRetries; attempt++ {
		if attempt > 0 {
			logger.DebugContext(ctx, "Retrying LDAP operation",
				"operation", operation,
				"attempt", attempt,
				"max_retries", l.MaxRetries,
				"delay", delay)
			time.Sleep(delay)

			// Exponential backoff with cap
			delay *= 2
			if delay > l.MaxRetryDelay {
				delay = l.MaxRetryDelay
			}
		}

		err := fn()
		if err == nil {
			if attempt > 0 {
				logger.InfoContext(ctx, "LDAP operation succeeded after retries",
					"operation", operation,
					"attempts", attempt)
			}

			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			logger.DebugContext(ctx, "Non-retryable error",
				"operation", operation,
				"error", err)

			return err
		}

		logger.WarnContext(ctx, "Transient error",
			"operation", operation,
			"error", err)
	}

	logger.ErrorContext(ctx, "LDAP operation failed after all retries",
		"operation", operation,
		"max_retries", l.MaxRetries,
		"error", lastErr)

	return fmt.Errorf("operation %s failed after %d retries: %w", operation, l.MaxRetries, lastErr)
}

// isRetryableError determines if an error is transient and should be retried.
// Non-retryable errors include validation errors, permission errors, and invalid keys.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Config errors (validation, permission, unknown keys) are not retryable
	if errs.IsConfigError(err) {
		return false
	}

	// In production, this would check for specific LDAP error codes:
	// - LDAP_SERVER_DOWN (0x51)
	// - LDAP_TIMEOUT (0x55)
	// - LDAP_CONNECT_ERROR (0x5b)
	// - LDAP_BUSY (0x33)
	// - LDAP_UNAVAILABLE (0x34)
	//
	// For now, we assume other errors are transient and retryable

	return true
}

// Domain represents a Carbonio domain with virtual hostname configuration
type Domain struct {
	DomainName       string
	VirtualHostname  string
	VirtualIPAddress string
	ClientCertMode   string
	SSLCertificate   string
	SSLPrivateKey    string
}

// Server represents a Carbonio server with service configuration
type Server struct {
	ServerID        string // zimbraId
	ServiceHostname string // zimbraServiceHostname
	// Attributes holds the full set of LDAP attributes for the server entry.
	// Multi-valued attributes are joined with "\n" (see entryToMap). Used by
	// the proxy upstream resolvers to read mail mode, ports, and proxy tuning
	// attributes without issuing additional per-server queries.
	Attributes map[string]string
}

// Helper function to simulate LDAP search for master check
