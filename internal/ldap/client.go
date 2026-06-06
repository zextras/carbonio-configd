// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package ldap provides native LDAP client for Carbonio LDAP operations.
package ldap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	errs "github.com/zextras/carbonio-configd/internal/errors"
	"github.com/zextras/carbonio-configd/internal/intern"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// Client represents a connection pool to the LDAP server.
type Client struct {
	urls     []string // Ordered list of LDAP URLs (failover at connect-time)
	bindDN   string   // Bind DN (e.g., uid=zimbra,cn=admins,cn=zimbra)
	password string   // Bind password
	baseDN   string   // Base DN (e.g., cn=zimbra)
	startTLS bool     // Whether to upgrade ldap:// connections with StartTLS

	// Connection pool
	pool     []*ldap.Conn
	poolSize int
	poolMu   sync.Mutex

	// Retry configuration
	maxRetries    int
	retryDelay    time.Duration
	maxRetryDelay time.Duration

	// dialTimeout bounds a single dial+TLS attempt so an unreachable or
	// hung LDAP server cannot block a config cycle indefinitely.
	dialTimeout time.Duration

	// opTimeout bounds each individual LDAP request (search/modify) once a
	// connection is established, so a server that accepts the connection but
	// then stalls mid-operation cannot block a config cycle indefinitely.
	opTimeout time.Duration

	// TLS configuration
	tlsConfig *tls.Config
}

// defaultBaseDN is the default LDAP Base DN used when ClientConfig.BaseDN
// is left empty.
const defaultBaseDN = "cn=zimbra"

// ldapFilterAllObjects is the LDAP filter used to match any object by DN.
const ldapFilterAllObjects = "(objectClass=*)"

// ClientConfig holds configuration for creating an LDAP client.
//
// URL and URLs are alternative inputs for the LDAP endpoint:
//   - If URLs is non-empty it takes precedence and the client uses
//     connect-time failover across the listed URLs in order.
//   - Otherwise URL is parsed via ParseURLs, so callers passing the raw
//     localconfig string (which may contain space-separated multi-URLs)
//     keep working without code changes.
type ClientConfig struct {
	URL           string        // LDAP URL (single URL, or space-separated list — see ParseURLs)
	URLs          []string      // Pre-parsed ordered URL list (preferred over URL when set)
	BindDN        string        // Bind DN
	Password      string        // Password
	BaseDN        string        // Base DN (defaults to "cn=zimbra" if empty)
	PoolSize      int           // Connection pool size (default: 5)
	MaxRetries    int           // Max retry attempts (default: 3)
	RetryDelay    time.Duration // Initial retry delay (default: 100ms)
	MaxRetryDelay time.Duration // Max retry delay (default: 5s)
	DialTimeout   time.Duration // Per-attempt dial timeout (default: 10s)
	OpTimeout     time.Duration // Per-request timeout for search/modify (default: 30s)
	TLSConfig     *tls.Config   // TLS configuration (optional)
	StartTLS      bool          // Upgrade ldap:// connections with StartTLS (default: true)
}

// defaultDialTimeout bounds a single LDAP dial attempt when
// ClientConfig.DialTimeout is left unset.
const defaultDialTimeout = 10 * time.Second

// defaultOpTimeout bounds a single LDAP request (search/modify) when
// ClientConfig.OpTimeout is left unset.
const defaultOpTimeout = 30 * time.Second

// NewClient creates a new LDAP client with connection pooling.
func NewClient(config *ClientConfig) (*Client, error) {
	if config.BaseDN == "" {
		config.BaseDN = defaultBaseDN
	}

	if config.PoolSize <= 0 {
		config.PoolSize = 5
	}

	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
	}

	if config.RetryDelay == 0 {
		config.RetryDelay = 100 * time.Millisecond
	}

	if config.MaxRetryDelay == 0 {
		config.MaxRetryDelay = 5 * time.Second
	}

	if config.DialTimeout == 0 {
		config.DialTimeout = defaultDialTimeout
	}

	if config.OpTimeout == 0 {
		config.OpTimeout = defaultOpTimeout
	}

	var tlsConfig *tls.Config
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig
	}

	urls := config.URLs
	if len(urls) == 0 {
		urls = ParseURLs(config.URL)
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("ldap client: no URL configured (URL=%q, URLs=%v)", config.URL, config.URLs)
	}

	client := &Client{
		urls:          urls,
		bindDN:        config.BindDN,
		password:      config.Password,
		baseDN:        config.BaseDN,
		startTLS:      config.StartTLS,
		poolSize:      config.PoolSize,
		maxRetries:    config.MaxRetries,
		retryDelay:    config.RetryDelay,
		maxRetryDelay: config.MaxRetryDelay,
		dialTimeout:   config.DialTimeout,
		opTimeout:     config.OpTimeout,
		tlsConfig:     tlsConfig,
		pool:          make([]*ldap.Conn, 0, config.PoolSize),
	}

	return client, nil
}

// getConnection gets a connection from the pool or creates a new one.
func (c *Client) getConnection() (*ldap.Conn, error) {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()

	if len(c.pool) > 0 {
		conn := c.pool[len(c.pool)-1]
		c.pool = c.pool[:len(c.pool)-1]

		if conn != nil && !conn.IsClosing() {
			return conn, nil
		}
	}

	conn, err := c.connect()
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// returnConnection returns a connection to the pool.
func (c *Client) returnConnection(conn *ldap.Conn) {
	if conn == nil || conn.IsClosing() {
		return
	}

	c.poolMu.Lock()
	defer c.poolMu.Unlock()

	if len(c.pool) < c.poolSize {
		c.pool = append(c.pool, conn)
	} else {
		_ = conn.Close() // Best effort close
	}
}

// connect establishes a connection to the LDAP server, walking the
// configured URL list and using the first URL that completes both dial
// and bind successfully. Per-URL failures are emitted at WARN so an
// operator can pinpoint a misconfigured entry even when the overall
// connection succeeds (CO-3565).
func (c *Client) connect() (*ldap.Conn, error) {
	ctx := logger.ContextWithComponentOnce(context.Background(), "ldap")

	agg := &AggregateDialError{Errors: make([]error, 0, len(c.urls))}

	for _, url := range c.urls {
		conn, err := c.dialAndBind(url)
		if err == nil {
			return conn, nil
		}

		logger.WarnContext(ctx, "LDAP URL failed, trying next", "url", url, "error", err)

		agg.Errors = append(agg.Errors, &urlDialError{URL: url, Err: err})
	}

	return nil, fmt.Errorf("failed to connect to LDAP server: %w", agg)
}

// clientDial is the dialer used by dialAndBind. Production points it at
// ldap.DialURL; tests override it with a stub that returns deterministic
// errors so the failover loop in connect() can be exercised without a
// running LDAP server.
var clientDial = ldap.DialURL

// dialAndBind performs the full dial+StartTLS+bind sequence for a single URL.
// Returns a bound connection on success; on any failure it closes any partial
// connection and returns the wrapped error.
func (c *Client) dialAndBind(url string) (*ldap.Conn, error) {
	var (
		conn *ldap.Conn
		err  error
	)

	dialer := ldap.DialWithDialer(&net.Dialer{Timeout: c.dialTimeout})

	switch {
	case strings.HasPrefix(url, "ldaps://"):
		conn, err = clientDial(url, dialer, ldap.DialWithTLSConfig(c.tlsConfig))
	case strings.HasPrefix(url, "ldapi://"):
		// Unix-socket connection to the local slapd-config backend. No TLS and
		// no StartTLS: the socket is already a trusted local channel gated by
		// filesystem permissions. go-ldap dials the path component as a unix
		// socket (e.g. ldapi:///run/carbonio/run/ldapi).
		conn, err = clientDial(url, dialer)
	case strings.HasPrefix(url, "ldap://"):
		conn, err = clientDial(url, dialer)
	default:
		return nil, fmt.Errorf("unsupported LDAP URL scheme: %s", url)
	}

	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	if strings.HasPrefix(url, "ldap://") && c.startTLS {
		if err := conn.StartTLS(c.tlsConfig); err != nil {
			_ = conn.Close()

			return nil, fmt.Errorf("StartTLS: %w", err)
		}
	}

	if err := conn.Bind(c.bindDN, c.password); err != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("bind: %w", err)
	}

	return conn, nil
}

// Close closes all connections in the pool.
func (c *Client) Close() error {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()

	for _, conn := range c.pool {
		if conn != nil && !conn.IsClosing() {
			_ = conn.Close() // Best effort close
		}
	}

	c.pool = nil

	return nil
}

// executeWithRetry executes an LDAP operation with retry logic.
func (c *Client) executeWithRetry(operation func(*ldap.Conn) error) error {
	var lastErr error

	delay := c.retryDelay

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay = c.nextRetryDelay(delay)
		}

		conn, err := c.getConnection()
		if err != nil {
			lastErr = err
			continue
		}

		// Bound this request so a server that stalls mid-operation cannot
		// block the caller indefinitely. SetTimeout applies to subsequent
		// requests on this connection.
		conn.SetTimeout(c.opTimeout)

		err = operation(conn)
		if err == nil {
			c.returnConnection(conn)
			return nil
		}

		lastErr = c.handleOperationError(conn, err)

		if !isLDAPErrorRetryable(lastErr) {
			return lastErr
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", c.maxRetries, lastErr)
}

// nextRetryDelay sleeps for delay and returns the next (exponentially backed-off) delay.
func (c *Client) nextRetryDelay(delay time.Duration) time.Duration {
	time.Sleep(delay)

	delay *= 2
	if delay > c.maxRetryDelay {
		return c.maxRetryDelay
	}

	return delay
}

// handleOperationError classifies the error: discards the connection when it is
// unhealthy, returns it to the pool otherwise. Returns the annotated error.
func (c *Client) handleOperationError(conn *ldap.Conn, err error) error {
	if isConnectionError(err) {
		// Discard unhealthy connection and tag the error so callers
		// can classify it via errors.Is(err, errs.ErrLDAPUnhealthyConnection).
		if conn != nil {
			_ = conn.Close()
		}

		return fmt.Errorf("%w: %w", errs.ErrLDAPUnhealthyConnection, err)
	}

	c.returnConnection(conn)

	return err
}

// isConnectionError determines if an error indicates a connection/transport failure
// that should cause the connection to be discarded rather than returned to the pool.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	ldapErr := &ldap.Error{}
	if errors.As(err, &ldapErr) {
		switch ldapErr.ResultCode {
		case ldap.LDAPResultServerDown:
			return true
		case ldap.LDAPResultTimeout:
			return true
		case ldap.LDAPResultUnavailable:
			return true
		case ldap.LDAPResultConnectError:
			return true
		default:
			return false
		}
	}

	errStr := err.Error()
	if strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "reset by peer") {
		return true
	}

	return false
}

// isLDAPErrorRetryable determines if an LDAP error should be retried.
// Permanent errors like "No Such Object" should not be retried.
func isLDAPErrorRetryable(err error) bool {
	if err == nil {
		return false
	}

	ldapErr := &ldap.Error{}
	if errors.As(err, &ldapErr) {
		switch ldapErr.ResultCode {
		case ldap.LDAPResultNoSuchObject:
			// Object doesn't exist - don't retry
			return false
		case ldap.LDAPResultInvalidDNSyntax:
			// Bad DN syntax - don't retry
			return false
		case ldap.LDAPResultInvalidCredentials:
			// Bad credentials - don't retry
			return false
		case ldap.LDAPResultInsufficientAccessRights:
			// Permission denied - don't retry
			return false
		case ldap.LDAPResultObjectClassViolation:
			// Schema violation - don't retry
			return false
		case ldap.LDAPResultServerDown:
			// Server down - retry
			return true
		case ldap.LDAPResultTimeout:
			// Timeout - retry
			return true
		case ldap.LDAPResultBusy:
			// Server busy - retry
			return true
		case ldap.LDAPResultUnavailable:
			// Server unavailable - retry
			return true
		case ldap.LDAPResultUnwillingToPerform:
			// Server unwilling - retry
			return true
		default:
			// Unknown error - retry to be safe
			return true
		}
	}

	// Non-LDAP errors (network, etc.) - retry
	return true
}

// Search performs an LDAP search with the given parameters.
func (c *Client) Search(baseDN, filter string, attributes []string, scope int) (*ldap.SearchResult, error) {
	var result *ldap.SearchResult

	err := c.executeWithRetry(func(conn *ldap.Conn) error {
		searchRequest := ldap.NewSearchRequest(
			baseDN,
			scope,
			ldap.NeverDerefAliases,
			0,     // No size limit
			0,     // No time limit
			false, // Types only: false
			filter,
			attributes,
			nil, // No controls
		)

		res, err := conn.Search(searchRequest)
		if err != nil {
			return err
		}

		result = res

		return nil
	})

	return result, err
}

// GetEntry retrieves a single LDAP entry by DN.
func (c *Client) GetEntry(dn string, attributes []string) (*ldap.Entry, error) {
	result, err := c.Search(dn, ldapFilterAllObjects, attributes, ldap.ScopeBaseObject)
	if err != nil {
		return nil, err
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("entry not found: %s", dn)
	}

	return result.Entries[0], nil
}

// ReadAttribute reads a single attribute value from an entry by DN using a
// base-scoped search. present reports whether the attribute exists on the
// entry, distinguishing an absent attribute from an empty value. Used for
// change-detection before a replace (mirrors jylibs/ldap.py).
func (c *Client) ReadAttribute(dn, attribute string) (value string, present bool, err error) {
	entry, err := c.GetEntry(dn, []string{attribute})
	if err != nil {
		return "", false, err
	}

	for _, a := range entry.Attributes {
		if a.Name != attribute {
			continue
		}

		if len(a.Values) > 0 {
			return a.Values[0], true, nil
		}

		return "", true, nil
	}

	return "", false, nil
}

// ProbeDN reports whether a base-scoped search on dn succeeds, i.e. the entry
// exists. Mirrors the cn=accesslog probe in jylibs/ldap.py used to confirm LDAP
// master status; any search error (including "no such object") yields false.
func (c *Client) ProbeDN(dn string) bool {
	_, err := c.Search(dn, ldapFilterAllObjects, []string{"1.1"}, ldap.ScopeBaseObject)

	return err == nil
}

// getEntityConfig is a helper function to retrieve config for a specific entity by DN.
// It eliminates duplicate code for GetServerConfig, GetDomain, etc.
func (c *Client) getEntityConfig(dn, entityType, entityName string) (map[string]string, error) {
	result, err := c.Search(dn, ldapFilterAllObjects, []string{"*"}, ldap.ScopeBaseObject)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s config for %s: %w", entityType, entityName, err)
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("%s not found: %s", entityType, entityName)
	}

	return entryToMap(result.Entries[0]), nil
}

// getEntityNames is a helper function to retrieve a list of entity names.
// It eliminates duplicate code for GetAllServers, GetAllDomains, etc.
func (c *Client) getEntityNames(dn, filter, attributeName, entityType string) ([]string, error) {
	result, err := c.Search(dn, filter, []string{attributeName}, ldap.ScopeSingleLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to get all %s: %w", entityType, err)
	}

	entities := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		name := entry.GetAttributeValue(attributeName)
		if name != "" {
			entities = append(entities, name)
		}
	}

	return entities, nil
}

// getEntitiesWithAttributes is a helper function to retrieve entities with full attributes.
// It eliminates duplicate code for GetAllServersWithAttributes, GetAllDomainsWithAttributes, etc.
func (c *Client) getEntitiesWithAttributes(
	dn, filter, keyAttribute, entityType string,
) (map[string]map[string]string, error) {
	result, err := c.Search(dn, filter, []string{"*"}, ldap.ScopeSingleLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to get all %s with attributes: %w", entityType, err)
	}

	entities := make(map[string]map[string]string, len(result.Entries))
	for _, entry := range result.Entries {
		keyValue := entry.GetAttributeValue(keyAttribute)
		if keyValue != "" {
			entities[keyValue] = entryToMap(entry)
		}
	}

	return entities, nil
}

// GetGlobalConfig retrieves the global configuration (cn=config,cn=zimbra).
// Returns a map of attribute name to value, matching zmprov gacf output format.
func (c *Client) GetGlobalConfig() (map[string]string, error) {
	dn := fmt.Sprintf("cn=config,%s", c.baseDN)

	result, err := c.Search(dn, ldapFilterAllObjects, []string{"*"}, ldap.ScopeBaseObject)
	if err != nil {
		return nil, fmt.Errorf("failed to get global config: %w", err)
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("global config not found")
	}

	return entryToMap(result.Entries[0]), nil
}

// GetServerConfig retrieves configuration for a specific server.
// Returns a map of attribute name to value, matching zmprov gs output format.
func (c *Client) GetServerConfig(hostname string) (map[string]string, error) {
	dn := fmt.Sprintf("cn=%s,cn=servers,%s", ldap.EscapeDN(hostname), c.baseDN)
	return c.getEntityConfig(dn, "server", hostname)
}

// GetAllServers retrieves all servers from LDAP.
// Returns a list of server hostnames, matching zmprov gas output format.
func (c *Client) GetAllServers() ([]string, error) {
	dn := fmt.Sprintf("cn=servers,%s", c.baseDN)
	return c.getEntityNames(dn, "(objectClass=zimbraServer)", "cn", "servers")
}

// GetAllServersWithAttributes retrieves all servers with full attributes.
// Returns a map of hostname to attributes, useful for zmprov gas -v equivalent.
func (c *Client) GetAllServersWithAttributes() (map[string]map[string]string, error) {
	dn := fmt.Sprintf("cn=servers,%s", c.baseDN)
	return c.getEntitiesWithAttributes(dn, "(objectClass=zimbraServer)", "cn", "servers")
}

// GetAllDomains retrieves all domains from LDAP.
// Returns a list of domain names, matching zmprov gad output format.
//
// Carbonio stores domain entries under their own DC-based subtrees
// (e.g. dc=example,dc=com), NOT under cn=domains,cn=zimbra. A subtree
// search from the root DSE with an objectClass filter is required.
func (c *Client) GetAllDomains() ([]string, error) {
	result, err := c.Search("", "(objectClass=zimbraDomain)", []string{"zimbraDomainName"}, ldap.ScopeWholeSubtree)
	if err != nil {
		return nil, fmt.Errorf("failed to get all domains: %w", err)
	}

	domains := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		name := entry.GetAttributeValue("zimbraDomainName")
		if name != "" {
			domains = append(domains, name)
		}
	}

	return domains, nil
}

// GetDomain retrieves configuration for a specific domain.
// Returns a map of attribute name to value, matching zmprov gd output format.
func (c *Client) GetDomain(domain string) (map[string]string, error) {
	filter := fmt.Sprintf("(&(objectClass=zimbraDomain)(zimbraDomainName=%s))", ldap.EscapeFilter(domain))

	result, err := c.Search("", filter, []string{"*"}, ldap.ScopeWholeSubtree)
	if err != nil {
		return nil, fmt.Errorf("failed to get domain config for %s: %w", domain, err)
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("domain not found: %s", domain)
	}

	return entryToMap(result.Entries[0]), nil
}

// GetAllDomainsWithAttributes retrieves all domains with full attributes in a single LDAP query.
// This is significantly faster than calling GetAllDomains() followed by GetDomain() for each domain.
// Returns a map of domain name to attributes, useful for batch domain queries.
func (c *Client) GetAllDomainsWithAttributes() (map[string]map[string]string, error) {
	result, err := c.Search("", "(objectClass=zimbraDomain)", []string{"*"}, ldap.ScopeWholeSubtree)
	if err != nil {
		return nil, fmt.Errorf("failed to get all domains with attributes: %w", err)
	}

	domains := make(map[string]map[string]string, len(result.Entries))
	for _, entry := range result.Entries {
		name := entry.GetAttributeValue("zimbraDomainName")
		if name != "" {
			domains[name] = entryToMap(entry)
		}
	}

	return domains, nil
}

// GetEnabledServices returns the list of enabled services for a server hostname.
// Queries the multi-valued zimbraServiceEnabled attribute from the server LDAP entry.
func (c *Client) GetEnabledServices(hostname string) ([]string, error) {
	dn := fmt.Sprintf("cn=%s,cn=servers,%s", ldap.EscapeDN(hostname), c.baseDN)

	result, err := c.Search(dn, ldapFilterAllObjects, []string{attrZimbraServiceEnabled}, ldap.ScopeBaseObject)
	if err != nil {
		return nil, fmt.Errorf("failed to get enabled services for %s: %w", hostname, err)
	}

	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("server not found: %s", hostname)
	}

	return result.Entries[0].GetAttributeValues(attrZimbraServiceEnabled), nil
}

// ModifyAttribute replaces an attribute value on an LDAP entry.
func (c *Client) ModifyAttribute(dn, attribute, value string) error {
	return c.executeWithRetry(func(conn *ldap.Conn) error {
		modifyRequest := ldap.NewModifyRequest(dn, nil)
		modifyRequest.Replace(attribute, []string{value})

		return conn.Modify(modifyRequest)
	})
}

// entryToMap converts an LDAP entry to a map of attribute name to value.
// For multi-valued attributes, values are joined with newlines to match zmprov output.
// Attribute names are interned so that every downstream map keyed by them
// shares the same backing storage across the process.
func entryToMap(entry *ldap.Entry) map[string]string {
	result := make(map[string]string, len(entry.Attributes))

	for _, attr := range entry.Attributes {
		name := intern.Attr(attr.Name)

		if len(attr.Values) == 1 {
			result[name] = attr.Values[0]
		} else if len(attr.Values) > 1 {
			// Multi-valued attributes: join with newlines
			result[name] = strings.Join(attr.Values, "\n")
		}
	}

	return result
}

// FormatAsZmprovOutput formats a config map as zmprov-style output.
// Each line is "key: value" to match zmprov output format.
func FormatAsZmprovOutput(config map[string]string) string {
	var builder strings.Builder

	for key, value := range config {
		// Handle multi-line values
		lines := strings.SplitSeq(value, "\n")
		for line := range lines {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}
