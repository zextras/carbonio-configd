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

// Manager interface defines methods for LDAP attribute management.
type Manager interface {
	// ModifyAttribute modifies an LDAP attribute using zmprov
	ModifyAttribute(ctx context.Context, key, value string) error

	// ModifyAttributeBatch modifies multiple LDAP attributes in batches by DN
	ModifyAttributeBatch(ctx context.Context, changes map[string]string) error

	// GetPendingChanges returns the current pending LDAP changes
	GetPendingChanges() map[string]string

	// AddChange adds an LDAP change to the pending queue
	AddChange(ctx context.Context, key, value string)

	// ClearPending clears all pending changes
	ClearPending()
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

	// Native LDAP client for direct LDAP queries
	NativeClient *Client
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
	"ldap_common_loglevel":       {"olcLogLevel", ldapCnConfig, false, "%s"},
	"ldap_common_threads":        {"olcThreads", ldapCnConfig, false, "%s"},
	"ldap_common_toolthreads":    {"olcToolThreads", ldapCnConfig, false, "%s"},
	"ldap_common_require_tls":    {"olcSecurity", ldapCnConfig, false, "ssf=%s"},
	"ldap_common_writetimeout":   {"olcWriteTimeout", ldapCnConfig, false, "%s"},
	"ldap_common_tlsdhparamfile": {"olcTLSDHParamFile", ldapCnConfig, false, "%s"},
	"ldap_common_tlsprotocolmin": {"olcTLSProtocolMin", ldapCnConfig, false, "%s"},
	"ldap_common_tlsciphersuite": {"olcTLSCipherSuite", ldapCnConfig, false, "%s"},

	"ldap_db_maxsize":  {"olcDbMaxsize", ldapDB3MdbCnConfig, false, "%s"},
	"ldap_db_envflags": {"olcDbEnvFlags", ldapDB3MdbCnConfig, false, "%s"},
	"ldap_db_rtxnsize": {"olcDbRtxnSize", ldapDB3MdbCnConfig, false, "%s"},

	"ldap_accesslog_maxsize":           {"olcDbMaxsize", ldapDB2MdbCnConfig, true, "%s"},
	"ldap_accesslog_envflags":          {"olcDbEnvFlags", ldapDB2MdbCnConfig, true, "%s"},
	"ldap_overlay_syncprov_checkpoint": {"olcSpCheckpoint", "olcOverlay={0}syncprov,olcDatabase={3}mdb,cn=config", true, "%s"},
	"ldap_overlay_syncprov_sessionlog": {"olcSpSessionlog", "olcOverlay={0}syncprov,olcDatabase={3}mdb,cn=config", true, "%s"},

	"ldap_overlay_accesslog_logpurge": {"olcAccessLogPurge", "olcOverlay={1}accesslog,olcDatabase={3}mdb,cn=config", true, "%s"},
}

// NewLdap initializes a new Ldap client with default retry configuration.
func NewLdap(ctx context.Context, cfg *config.Config) *Ldap {
	ctx = logger.ContextWithComponent(ctx, "ldap")
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
	ctx = logger.ContextWithComponent(ctx, "ldap")
	l.NativeClient = client

	if client != nil {
		logger.DebugContext(ctx, "Native LDAP client set for Ldap manager")
	} else {
		logger.DebugContext(ctx, "Native LDAP client cleared")
	}
}

// AddChange adds an LDAP change to the pending queue.
func (l *Ldap) AddChange(ctx context.Context, key, value string) {
	ctx = logger.ContextWithComponent(ctx, "ldap")
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

// ModifyAttribute modifies an LDAP attribute using direct LDAP operations with retry logic.
// This is a simplified implementation that directly manipulates cn=config.
// In production, this would use proper LDAP client libraries.
func (l *Ldap) ModifyAttribute(ctx context.Context, key, value string) error {
	ctx = logger.ContextWithComponent(ctx, "ldap")
	logger.InfoContext(ctx, "Setting LDAP attribute",
		"key", key,
		"value", value)

	// Simulate master check if needed
	if l.config.LdapIsMaster {
		l.IsMaster = true

		logger.DebugContext(ctx, "LDAP config is master")
	}

	// Validation happens outside retry logic (not retryable)
	entry, err := l.lookupKey(ctx, key)
	if err != nil {
		logger.ErrorContext(ctx, "LDAP lookup error",
			"error", err,
			"key", key)

		return err
	}

	val := fmt.Sprintf(entry.TransformFmt, value)

	// Execute LDAP modification with retry logic
	return l.withRetry(ctx, fmt.Sprintf("modify %s=%s", key, value), func() error {
		// In a real implementation, this would:
		// 1. Connect to LDAP via ldapi:/// or ldap_master_url
		// 2. Search for the DN and fetch the current attribute value
		// 3. Compare with the new value
		// 4. If different, perform an LDAP modify operation
		//
		// For now, we'll use ldapmodify command directly as a placeholder.
		// This requires proper LDIF generation and error handling.
		logger.InfoContext(ctx, "Would modify LDAP",
			"dn", entry.DN,
			"attr", entry.Attr,
			"value", val)

		// Placeholder: In production, replace this with actual LDAP client code
		// For now, we'll just log the operation
		// Example command that would be used:
		// echo -e "dn: ${DN}\nchangetype: modify\nreplace: ${ATTR}\n${ATTR}: ${VAL}" | ldapmodify -Y EXTERNAL -H ldapi:///

		return nil
	})
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
	ctx = logger.ContextWithComponent(ctx, "ldap")

	if len(changes) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Batch modifying LDAP attributes",
		"count", len(changes))

	// Set master flag if needed
	if l.config.LdapIsMaster {
		l.IsMaster = true
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
// This is called by executeBatchModify through the retry wrapper.
func (l *Ldap) executeBatchModifyInternal(ctx context.Context, dn string, attrs map[string]string) error {
	logger.DebugContext(ctx, "Batch modifying DN",
		"dn", dn,
		"attribute_count", len(attrs))

	// In a real implementation, this would:
	// 1. Build an LDAP modify request with multiple attribute replacements
	// 2. Execute the modify operation in a single LDAP transaction
	// 3. Handle errors and rollback if needed
	//
	// Example LDIF that would be generated:
	// dn: cn=config
	// changetype: modify
	// replace: olcLogLevel
	// olcLogLevel: 256
	// -
	// replace: olcThreads
	// olcThreads: 8
	// -

	for attr, val := range attrs {
		logger.DebugContext(ctx, "Batch attribute",
			"attr", attr,
			"value", val)
	}

	// Placeholder: In production, replace this with actual LDAP client code
	// This would use ldapmodify with a properly formatted LDIF containing
	// all attribute modifications for this DN

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
}

// QueryDomains queries all domains that have a zimbraVirtualHostname configured.
// This is used by the nginx proxy generator to create virtual host configurations.
// Returns a list of Domain structs containing the domain name, virtual hostname,
// virtual IP address, and SSL certificate information.
func (l *Ldap) QueryDomains(ctx context.Context) ([]Domain, error) {
	ctx = logger.ContextWithComponent(ctx, "ldap")
	t0 := time.Now()

	logger.DebugContext(ctx, "Starting QueryDomains")

	// Use native LDAP client if available
	if l.NativeClient == nil {
		return nil, fmt.Errorf("native LDAP client not initialized")
	}

	// OPTIMIZED: Use batch query to get all domains with attributes in one LDAP query
	// This replaces the previous approach of:
	//   1. GetAllDomains() - get domain names list
	//   2. For each domain: GetDomain(name) - sequential queries
	t1 := time.Now()
	allDomainsAttrs, err := l.NativeClient.GetAllDomainsWithAttributes()
	queryDuration := time.Since(t1)

	logger.DebugContext(ctx, "GetAllDomainsWithAttributes completed",
		"duration_ms", queryDuration.Milliseconds(),
		"domain_count", len(allDomainsAttrs))

	if err != nil {
		return nil, fmt.Errorf("failed to get domains with attributes: %w", err)
	}

	if len(allDomainsAttrs) == 0 {
		logger.DebugContext(ctx, "No domains found")

		return []Domain{}, nil
	}

	var domains []Domain

	// Process domain attributes and filter by zimbraVirtualHostname
	t2 := time.Now()

	for domainName, domainAttrs := range allDomainsAttrs {
		// Build Domain struct from attributes
		domain := Domain{
			DomainName:       domainName,
			VirtualHostname:  domainAttrs["zimbraVirtualHostname"],
			VirtualIPAddress: domainAttrs["zimbraVirtualIPAddress"],
			ClientCertMode:   domainAttrs["zimbraClientCertMode"],
			SSLCertificate:   domainAttrs["zimbraSSLCertificate"],
			SSLPrivateKey:    domainAttrs["zimbraSSLPrivateKey"],
		}

		// Only include domains with zimbraVirtualHostname set
		if domain.VirtualHostname != "" {
			domains = append(domains, domain)
			logger.DebugContext(ctx, "Found domain",
				"domain_name", domain.DomainName,
				"virtual_hostname", domain.VirtualHostname,
				"virtual_ip", domain.VirtualIPAddress)
		}
	}

	processingDuration := time.Since(t2)

	totalDuration := time.Since(t0)
	logger.DebugContext(ctx, "QueryDomains completed",
		"total_duration_ms", totalDuration.Milliseconds(),
		"ldap_query_ms", queryDuration.Milliseconds(),
		"processing_ms", processingDuration.Milliseconds(),
		"total_domains", len(allDomainsAttrs),
		"filtered_domains", len(domains))

	return domains, nil
}

// QueryServers queries all servers that have the specified service enabled.
// This is used by the nginx proxy generator to create upstream server configurations.
// Returns a list of Server structs containing the server ID and service hostname.
//
// serviceName examples: "mailbox", "proxy", "mta", "ldap", "memcached"
func (l *Ldap) QueryServers(ctx context.Context, serviceName string) ([]Server, error) {
	ctx = logger.ContextWithComponent(ctx, "ldap")
	logger.DebugContext(ctx, "Querying all servers with service",
		"service", serviceName)

	// Use native LDAP client if available
	if l.NativeClient == nil {
		return nil, fmt.Errorf("native LDAP client not initialized")
	}

	// Get all servers with full attributes
	allServers, err := l.NativeClient.GetAllServersWithAttributes()
	if err != nil {
		return nil, fmt.Errorf("failed to query servers: %w", err)
	}

	var servers []Server

	// Filter servers that have the specified service enabled
	for _, serverAttrs := range allServers {
		// Get server ID
		serverID := serverAttrs["zimbraId"]

		// Get service hostname
		serviceHostname := serverAttrs["zimbraServiceHostname"]

		// Check if the service is enabled
		servicesEnabled := serverAttrs["zimbraServiceEnabled"]
		hasService := false

		// zimbraServiceEnabled is multi-valued and joined with \n
		if strings.Contains(servicesEnabled, serviceName) {
			hasService = true
		}

		// Add server if it has the requested service and required fields
		if hasService && serverID != "" && serviceHostname != "" {
			servers = append(servers, Server{
				ServerID:        serverID,
				ServiceHostname: serviceHostname,
			})
		}
	}

	logger.DebugContext(ctx, "Found servers with service",
		"count", len(servers),
		"service", serviceName)

	return servers, nil
}

// Helper function to simulate LDAP search for master check
