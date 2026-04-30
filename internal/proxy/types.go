// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy implements nginx proxy configuration generation equivalent to
// Java ProxyConfGen functionality. It handles variable expansion, template
// processing, upstream server discovery, and SSL certificate mapping for
// nginx reverse proxy configuration.
package proxy

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
)

// ValueType represents the type of a proxy configuration variable
type ValueType int

const (
	// ValueTypeInteger represents an integer value
	ValueTypeInteger ValueType = iota
	// ValueTypeLong represents a long integer value
	ValueTypeLong
	// ValueTypeString represents a string value
	ValueTypeString
	// ValueTypeBoolean represents a boolean value
	ValueTypeBoolean
	// ValueTypeEnabler represents an enabler/disabler value
	ValueTypeEnabler
	// ValueTypeTime represents a time value
	ValueTypeTime
	// ValueTypeTimeInSec represents a time value in milliseconds that should be converted to seconds
	// This is equivalent to Java's TimeInSecVarWrapper: LDAP stores milliseconds, nginx expects plain seconds
	ValueTypeTimeInSec
	// ValueTypeCustom represents a custom computed value
	ValueTypeCustom
)

// OverrideType represents where a variable's value can be overridden from
type OverrideType int

const (
	// OverrideNone means the variable cannot be overridden
	OverrideNone OverrideType = iota
	// OverrideConfig means the variable can be overridden from global config
	OverrideConfig
	// OverrideServer means the variable can be overridden from server config
	OverrideServer
	// OverrideLocalConfig means the variable can be overridden from local config
	OverrideLocalConfig
	// OverrideCustom means the variable has custom override logic
	OverrideCustom
)

// Variable represents a proxy configuration variable
type Variable struct {
	// Keyword is the unique identifier for this variable (e.g., "web.http.port")
	Keyword string

	// Attribute is the LDAP attribute name (e.g., "zimbraMailProxyPort")
	Attribute string

	// ValueType is the type of the variable's value
	ValueType ValueType

	// OverrideType indicates where the value can be overridden from
	OverrideType OverrideType

	// DefaultValue is the default value if not configured
	DefaultValue any

	// Value is the current value (may be default or overridden)
	Value any

	// Description describes what this variable controls
	Description string

	// CustomFormatter is an optional function to format the value for output
	CustomFormatter func(any) (string, error)

	// CustomResolver is an optional function to resolve the value.
	// Resolvers are methods on *Generator, so the generator is available via the receiver.
	CustomResolver func(context.Context) (any, error)
}

// DomainAttr represents domain-specific attributes for virtual host configuration
type DomainAttr struct {
	// DomainName is the domain name
	DomainName string

	// VirtualHostname is the virtual hostname for this domain
	VirtualHostname []string

	// SSLCertificate is the path to the SSL certificate
	SSLCertificate string

	// SSLPrivateKey is the path to the SSL private key
	SSLPrivateKey string

	// ClientCertMode is the client certificate authentication mode
	ClientCertMode string

	// ClientCertCA is the path to the client CA certificate
	ClientCertCA string
}

// ServerAttr represents server-specific attributes for upstream configuration
type ServerAttr struct {
	// ServerName is the server hostname
	ServerName string

	// ServiceHostname is the service hostname (may differ from server name)
	ServiceHostname string

	// EnabledServices is the list of enabled services on this server
	EnabledServices []string

	// MailboxPort is the HTTP mailbox port
	MailboxPort int

	// MailboxSSLPort is the HTTPS mailbox port
	MailboxSSLPort int

	// AdminPort is the admin console HTTP port
	AdminPort int

	// AdminSSLPort is the admin console HTTPS port
	AdminSSLPort int
}

// Generator is the main proxy configuration generator
type Generator struct {
	// Config is the configd configuration
	Config *config.Config

	// LocalConfig contains local configuration values
	LocalConfig *config.LocalConfig

	// GlobalConfig contains global LDAP configuration
	GlobalConfig *config.GlobalConfig

	// ServerConfig contains server-specific LDAP configuration
	ServerConfig *config.ServerConfig

	// LdapClient is the LDAP client for queries
	LdapClient *ldap.Ldap

	// Cache is the configuration cache for LDAP queries
	Cache *cache.ConfigCache

	// upstreamCache is the cache for upstream query results (nginx proxy backends)
	// This prevents repeated expensive zmprov calls during template generation
	upstreamCache *upstreamQueryCache

	// Variables is the map of all proxy configuration variables
	Variables map[string]*Variable

	// DomainVariables is the map of domain-specific variables
	DomainVariables map[string]*Variable

	// Domains is the list of all domains with their attributes
	Domains []DomainAttr

	// Servers is the list of all servers with their attributes
	Servers []ServerAttr

	// WorkingDir is the working directory (default /opt/zextras)
	WorkingDir string

	// TemplateDir is the template directory
	TemplateDir string

	// ConfDir is the configuration directory
	ConfDir string

	// IncludesDir is the nginx includes directory
	IncludesDir string

	// DryRun indicates whether to actually write files
	DryRun bool

	// Verbose enables verbose logging output
	Verbose bool

	// Hostname is the hostname to generate configuration for
	Hostname string
}

// UpstreamServer represents an upstream server in an nginx upstream block
type UpstreamServer struct {
	// Host is the server hostname or IP address
	Host string

	// Port is the server port
	Port int

	// Weight is the load balancing weight (optional)
	Weight int

	// MaxFails is the number of failed attempts before marking as unavailable
	MaxFails int

	// FailTimeout is the time to consider server unavailable after max fails
	FailTimeout string
}

// Upstream represents an nginx upstream block
type Upstream struct {
	// Name is the upstream name (e.g., "zimbra", "zimbra_ssl")
	Name string

	// Servers is the list of upstream servers
	Servers []UpstreamServer

	// Keepalive is the keepalive connections setting
	Keepalive int
}

// upstreamQueryCache caches expensive zmprov query results
// This prevents repeated upstream server lookups during template generation
type upstreamQueryCache struct {
	// reverseProxyBackends caches getAllReverseProxyBackends() result
	reverseProxyBackends []UpstreamServer
	// reverseProxyBackendsSSL caches getAllReverseProxyBackendsSSL() result
	reverseProxyBackendsSSL []UpstreamServer
	// memcachedServers caches getAllMemcachedServers() result
	memcachedServers []MemcacheServer
	// attributeServers caches attribute-based queries (e.g., zimbraReverseProxyUpstreamEwsServers)
	// Map key is attribute name, value is list of servers
	attributeServers map[string][]UpstreamServer
	// attributeServersSSL caches SSL attribute-based queries
	attributeServersSSL map[string][]UpstreamServer
	// gasOutput stores the raw output from "zmprov gas -v" to avoid repeated calls
	// This is populated once and reused for all attribute-based queries
	gasOutput string
	// populated indicates whether the cache has been populated
	populated bool
}
