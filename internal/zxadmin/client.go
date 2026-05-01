// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package zxadmin is a minimal Go client for the Carbonio admin HTTP endpoints.
//
// It replaces shelling out to `/opt/zextras/bin/carbonio --json core ...`,
// which pays a 1-3s JVM cold-start cost on every invocation.
//
// Authentication uses the `urn:zimbraAdmin` AuthRequest with the LDAP master
// credentials from localconfig (zimbra_ldap_user / zimbra_ldap_password). The
// returned token is sent as ZM_ADMIN_AUTH_TOKEN cookie on subsequent calls.
package zxadmin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// AdminPort is the Carbonio admin HTTPS port.
	AdminPort = 7071

	soapPath   = "/service/admin/soap"
	statusPath = "/service/extension/zextras"

	authCookieName = "ZM_ADMIN_AUTH_TOKEN"

	defaultTimeout = 10 * time.Second

	// renewSkew is subtracted from the server-reported lifetime so we
	// re-authenticate before the token actually expires.
	renewSkew = 30 * time.Second
)

// ModuleStatus is one row in the `core getAllServicesStatus` response.
type ModuleStatus struct {
	Name           string // module name key (e.g. "core", "auth")
	CommercialName string // human label (e.g. "Core", "Auth")
	Running        bool
}

// Client talks to the local Carbonio admin endpoint over HTTPS.
//
// The zero value is not usable; use New.
type Client struct {
	baseURL    string
	httpClient *http.Client
	username   string
	password   string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// New returns a Client wired to https://localhost:7071 using the LDAP master
// credentials from a localconfig map.
func New(localCfg map[string]string) *Client {
	return NewWithBaseURL(fmt.Sprintf("https://localhost:%d", AdminPort), localCfg)
}

// NewWithBaseURL is like New but lets callers override the base URL (used by tests).
func NewWithBaseURL(baseURL string, localCfg map[string]string) *Client {
	user := localCfg["zimbra_ldap_user"]
	if user == "" {
		// localconfig.xml typically does not define zimbra_ldap_user; the
		// LDAP master account is "zimbra" (matches LC.zimbra_ldap_user
		// default in carbonio-mailbox).
		user = "zimbra"
	}

	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: user,
		password: localCfg["zimbra_ldap_password"],
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				// #nosec G402 - the admin endpoint serves a self-signed cert;
				// we are talking to localhost over a Unix-style trust boundary.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// authenticate performs a SOAP AdminAuthRequest and caches the returned token.
// Callers must hold c.mu.
func (c *Client) authenticate(ctx context.Context) error {
	if c.username == "" || c.password == "" {
		return fmt.Errorf("zxadmin: missing zimbra_ldap_user / zimbra_ldap_password in localconfig")
	}

	body := buildAuthEnvelope(c.username, c.password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+soapPath, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("zxadmin: build auth request: %w", err)
	}

	req.Header.Set("Content-Type", "text/xml; charset=UTF-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("zxadmin: auth request failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("zxadmin: read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zxadmin: auth failed: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}

	token, lifetimeMs, err := parseAuthResponse(raw)
	if err != nil {
		return err
	}

	c.token = token

	if lifetimeMs > 0 {
		c.expiresAt = time.Now().Add(time.Duration(lifetimeMs)*time.Millisecond - renewSkew)
	} else {
		// Conservative fallback if the server omits lifetime.
		c.expiresAt = time.Now().Add(1 * time.Hour)
	}

	return nil
}

// ensureToken refreshes the cached token if it is missing or near expiry.
// Caller must hold c.mu.
func (c *Client) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.expiresAt) {
		return nil
	}

	return c.authenticate(ctx)
}

// GetAllServicesStatus calls the `core getAllServicesStatus` action and
// returns one entry per advanced module.
func (c *Client) GetAllServicesStatus(ctx context.Context) ([]ModuleStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	// Parameters MUST be in the query string; the dispatcher does not parse
	// form-encoded bodies. The module name is "ZxCore" (the registered module
	// id), not the lowercase command name.
	q := url.Values{}
	q.Set("module", "ZxCore")
	q.Set("action", "getAllServicesStatus")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+statusPath+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("zxadmin: build status request: %w", err)
	}

	req.AddCookie(&http.Cookie{Name: authCookieName, Value: c.token})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zxadmin: status request failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("zxadmin: read status response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Token may have been invalidated server-side; drop it so the next
		// call re-authenticates.
		c.token = ""
		c.expiresAt = time.Time{}

		return nil, fmt.Errorf("zxadmin: status auth rejected: HTTP %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zxadmin: status request failed: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}

	return parseStatusResponse(raw)
}

// --- SOAP envelope construction & parsing ---------------------------------

func buildAuthEnvelope(user, pass string) string {
	var b strings.Builder

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">`)
	b.WriteString(`<soap:Body>`)
	b.WriteString(`<AuthRequest xmlns="urn:zimbraAdmin">`)
	b.WriteString(`<name>`)
	xmlEscape(&b, user)
	b.WriteString(`</name>`)
	b.WriteString(`<password>`)
	xmlEscape(&b, pass)
	b.WriteString(`</password>`)
	b.WriteString(`</AuthRequest>`)
	b.WriteString(`</soap:Body>`)
	b.WriteString(`</soap:Envelope>`)

	return b.String()
}

// xmlEscape writes s to b with XML-special characters escaped.
func xmlEscape(b *strings.Builder, s string) {
	_ = xml.EscapeText(stringWriter{b}, []byte(s))
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// authEnvelope is a minimal decoder for the AuthResponse SOAP envelope.
type authEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		AuthResponse struct {
			// Carbonio returns <lifetime> as a child element; older Zimbra
			// builds expose it as an attribute. Accept either.
			LifetimeAttr int64  `xml:"lifetime,attr"`
			LifetimeElem int64  `xml:"lifetime"`
			AuthToken    string `xml:"authToken"`
		} `xml:"AuthResponse"`
		Fault *struct {
			FaultCode   string `xml:"faultcode"`
			FaultString string `xml:"faultstring"`
			Reason      struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

func parseAuthResponse(raw []byte) (token string, lifetimeMs int64, err error) {
	var env authEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		return "", 0, fmt.Errorf("zxadmin: parse auth response: %w", err)
	}

	if env.Body.Fault != nil {
		msg := env.Body.Fault.Reason.Text
		if msg == "" {
			msg = env.Body.Fault.FaultString
		}

		return "", 0, fmt.Errorf("zxadmin: auth fault: %s", msg)
	}

	if env.Body.AuthResponse.AuthToken == "" {
		return "", 0, fmt.Errorf("zxadmin: empty authToken in response")
	}

	lifetime := env.Body.AuthResponse.LifetimeElem
	if lifetime == 0 {
		lifetime = env.Body.AuthResponse.LifetimeAttr
	}

	return env.Body.AuthResponse.AuthToken, lifetime, nil
}

// --- status response parsing -----------------------------------------------

// statusEntry is one decoded value in the getAllServicesStatus response.
type statusEntry struct {
	CommercialName string `json:"commercialName"`
	CommandName    string `json:"commandName"`
	Running        bool   `json:"running"`
}

// parseStatusResponse decodes the getAllServicesStatus body.
//
// The handler returns a JSON object keyed by module name:
//
//	{ "core": {"commercialName":"Core","running":true,...}, ... }
//
// The server may wrap that in {"ok":true,"response":{...}}. We try the
// wrapped form first, then fall back to the bare object.
func parseStatusResponse(raw []byte) ([]ModuleStatus, error) {
	// Try wrapped form: {"ok":true,"response":{...}}
	var wrapped struct {
		Response map[string]statusEntry `json:"response"`
	}

	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Response) > 0 {
		return mapToModules(wrapped.Response), nil
	}

	// Fall back to bare object: {"core":{...},"auth":{...}}
	var bare map[string]statusEntry
	if err := json.Unmarshal(raw, &bare); err != nil {
		return nil, fmt.Errorf("zxadmin: parse status response: %w (body=%s)", err, truncate(string(raw), 256))
	}

	if len(bare) == 0 {
		return nil, fmt.Errorf("zxadmin: empty status response")
	}

	return mapToModules(bare), nil
}

func mapToModules(m map[string]statusEntry) []ModuleStatus {
	out := make([]ModuleStatus, 0, len(m))

	for key, e := range m {
		// Prefer the lowercase commandName (e.g. "core"); fall back to the
		// map key (e.g. "ZxCore") only if the server omits it.
		name := e.CommandName
		if name == "" {
			name = key
		}

		commercial := e.CommercialName
		if commercial == "" {
			commercial = name
		}

		out = append(out, ModuleStatus{
			Name:           name,
			CommercialName: commercial,
			Running:        e.Running,
		})
	}

	return out
}

// --- helpers ---------------------------------------------------------------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "..."
}
