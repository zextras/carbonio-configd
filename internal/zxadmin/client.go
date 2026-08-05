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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// ErrAdvancedNotRunning reports that mailboxd is up but the Carbonio Advanced
// extension has no HTTP handler registered at statusPath — mailboxd answers
// 404 and logs "Extension HTTP handler not found at /zextras". That means the
// extension failed to boot (missing service-discover token, unreachable
// Consul KV, no Advanced DB, …), not that configd asked the wrong endpoint.
var ErrAdvancedNotRunning = errors.New("advanced extension handler not registered (HTTP 404)")

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

	// defaultUsername is the default LDAP master account username.
	defaultUsername = "zimbra"
)

// Retry tuning for transient HTTP failures (network errors, 5xx responses)
// talking to the local admin endpoint. Mirrors the exponential-backoff
// shape of ldap.Client.executeWithRetry; kept as local constants since this
// client has no connection pool to size a config struct around.
const (
	maxHTTPRetries    = 3
	httpRetryDelay    = 100 * time.Millisecond
	maxHTTPRetryDelay = 2 * time.Second
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
		user = defaultUsername
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
	ctx = logger.ContextWithComponentOnce(ctx, "zxadmin")

	if c.username == "" || c.password == "" {
		logger.WarnContext(ctx, "zxadmin: missing LDAP master credentials in localconfig")

		return fmt.Errorf("zxadmin: missing zimbra_ldap_user / zimbra_ldap_password in localconfig")
	}

	body := buildAuthEnvelope(c.username, c.password)

	resp, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+soapPath, strings.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("zxadmin: build auth request: %w", err)
		}

		req.Header.Set("Content-Type", "text/xml; charset=UTF-8")

		return req, nil
	})
	if err != nil {
		logger.WarnContext(ctx, "zxadmin: auth request failed", "error", err)

		return fmt.Errorf("zxadmin: auth request failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.WarnContext(ctx, "zxadmin: read auth response failed", "error", err)

		return fmt.Errorf("zxadmin: read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.WarnContext(ctx, "zxadmin: auth failed",
			"status", resp.StatusCode, "body", truncate(string(raw)))

		return fmt.Errorf("zxadmin: auth failed: HTTP %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	token, lifetimeMs, err := parseAuthResponse(raw)
	if err != nil {
		logger.WarnContext(ctx, "zxadmin: parse auth response failed", "error", err)

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
	ctx = logger.ContextWithComponentOnce(ctx, "zxadmin")

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

	statusURL := c.baseURL + statusPath + "?" + q.Encode()
	token := c.token

	resp, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, statusURL, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("zxadmin: build status request: %w", err)
		}

		// Defensive flags: the request goes over HTTPS to localhost:7071 and is
		// never seen by a browser, so HttpOnly/SameSite are no-ops here, but
		// setting them costs nothing and keeps gosec happy.
		req.AddCookie(&http.Cookie{
			Name:     authCookieName,
			Value:    token,
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})

		return req, nil
	})
	if err != nil {
		logger.WarnContext(ctx, "zxadmin: status request failed", "error", err)

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

		logger.WarnContext(ctx, "zxadmin: status auth rejected, dropping cached token",
			"status", resp.StatusCode)

		return nil, fmt.Errorf("zxadmin: status auth rejected: HTTP %d", resp.StatusCode)
	}

	// 404 means the extension handler is absent — report it as "not running"
	// instead of echoing mailboxd's HTML error page into `zmcontrol status`.
	if resp.StatusCode == http.StatusNotFound {
		logger.WarnContext(ctx, "zxadmin: advanced extension handler not registered (HTTP 404)")

		return nil, ErrAdvancedNotRunning
	}

	if resp.StatusCode != http.StatusOK {
		logger.WarnContext(ctx, "zxadmin: status request returned unexpected status",
			"status", resp.StatusCode, "body", truncate(string(raw)))

		return nil, fmt.Errorf("zxadmin: status request failed: HTTP %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	return parseStatusResponse(raw)
}

// doWithRetry sends the request built by newReq, retrying on network errors
// and 5xx responses with bounded exponential backoff (mirrors the shape of
// ldap.Client.executeWithRetry). 401/403/404 and other 4xx responses are
// never retried since they signal a request/auth problem a retry cannot
// fix. newReq is invoked once per attempt because an *http.Request's body
// cannot be replayed once sent. ctx cancellation is checked between
// attempts so callers are never blocked past their deadline.
func (c *Client) doWithRetry(ctx context.Context, newReq func() (*http.Request, error)) (*http.Response, error) {
	logCtx := logger.ContextWithComponentOnce(ctx, "zxadmin")

	delay := httpRetryDelay

	var lastErr error

	for attempt := 0; attempt <= maxHTTPRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			delay *= 2
			if delay > maxHTTPRetryDelay {
				delay = maxHTTPRetryDelay
			}
		}

		req, err := newReq()
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err

			logger.DebugContext(logCtx, "zxadmin: HTTP request failed, retrying",
				"attempt", attempt+1, "max_attempts", maxHTTPRetries+1, "error", err)

			continue
		}

		if resp.StatusCode >= http.StatusInternalServerError {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			_ = resp.Body.Close()

			logger.DebugContext(logCtx, "zxadmin: HTTP server error, retrying",
				"attempt", attempt+1, "max_attempts", maxHTTPRetries+1, "status", resp.StatusCode)

			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("zxadmin: request failed after %d attempts: %w", maxHTTPRetries+1, lastErr)
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
		return nil, fmt.Errorf("zxadmin: parse status response: %w (body=%s)", err, truncate(string(raw)))
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

	// Sort by Name for deterministic output — Go map iteration is randomized,
	// so without this the advanced-services list reorders on every status call.
	// Compare case-insensitively because the server returns mixed-case command
	// names (e.g. "sProxyd") and the display layer lowercases them, so a plain
	// byte-wise sort would put "sProxyd" before "admin".
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	return out
}

// --- helpers ---------------------------------------------------------------

const truncateLimit = 256

func truncate(s string) string {
	if len(s) <= truncateLimit {
		return s
	}

	return s[:truncateLimit] + "..."
}
