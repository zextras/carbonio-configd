// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const goodAuthResponse = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <AuthResponse xmlns="urn:zimbraAdmin" lifetime="43200000">
      <authToken>fake-token-xyz</authToken>
    </AuthResponse>
  </soap:Body>
</soap:Envelope>`

const faultResponse = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <soap:Fault>
      <faultcode>soap:Client</faultcode>
      <faultstring>authentication failed for [admin]</faultstring>
      <detail>
        <Reason><Text>authentication failed for [admin]</Text></Reason>
      </detail>
    </soap:Body>
  </soap:Body>
</soap:Envelope>`

// objectStatusResponse mirrors the real Carbonio handler: keys are the
// internal module ids (ZxCore, ZxAuth) and `commandName` carries the
// lowercase display name.
const objectStatusResponse = `{
  "ZxCore": {"commercialName":"Core","commandName":"core","running":true,"ModuleEnabledAtStartup":true},
  "ZxAuth": {"commercialName":"Auth","commandName":"auth","running":false,"ModuleEnabledAtStartup":true}
}`

const wrappedStatusResponse = `{"ok":true,"response":` + objectStatusResponse + `}`

// newTestClient wires a Client to an httptest server, accepting its self-signed cert.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()

	c := NewWithBaseURL(srv.URL, map[string]string{
		"zimbra_ldap_user":     "zimbra",
		"zimbra_ldap_password": "secret",
	})

	c.httpClient = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			// #nosec G402 - test client trusts httptest cert
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	return c
}

func TestGetAllServicesStatus_HappyPath_ObjectForm(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<name>zimbra</name>") {
			t.Errorf("auth body missing username, got %s", body)
		}
		w.Header().Set("Content-Type", "text/xml; charset=UTF-8")
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("module"); got != "ZxCore" {
			t.Errorf("module=%q, want ZxCore", got)
		}
		if got := q.Get("action"); got != "getAllServicesStatus" {
			t.Errorf("action=%q, want getAllServicesStatus", got)
		}
		ck, err := r.Cookie(authCookieName)
		if err != nil || ck.Value != "fake-token-xyz" {
			t.Errorf("missing/invalid auth cookie: %v / %v", ck, err)
		}
		_, _ = w.Write([]byte(objectStatusResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	mods, err := c.GetAllServicesStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2: %+v", len(mods), mods)
	}

	byName := map[string]ModuleStatus{}
	for _, m := range mods {
		byName[m.Name] = m
	}

	if !byName["core"].Running {
		t.Errorf("expected core running")
	}

	if byName["auth"].Running {
		t.Errorf("expected auth stopped")
	}

	if byName["core"].CommercialName != "Core" {
		t.Errorf("commercialName=%q, want Core", byName["core"].CommercialName)
	}
}

func TestGetAllServicesStatus_WrappedResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(wrappedStatusResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	mods, err := c.GetAllServicesStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2", len(mods))
	}
}

func TestGetAllServicesStatus_TokenReuse(t *testing.T) {
	authCalls := 0
	statusCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		authCalls++
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		statusCalls++
		_, _ = w.Write([]byte(objectStatusResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	for range 3 {
		if _, err := c.GetAllServicesStatus(context.Background()); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	}

	if authCalls != 1 {
		t.Errorf("authCalls=%d, want 1 (token should be cached)", authCalls)
	}

	if statusCalls != 3 {
		t.Errorf("statusCalls=%d, want 3", statusCalls)
	}
}

func TestGetAllServicesStatus_AuthFault(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(faultResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	_, err := c.GetAllServicesStatus(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestGetAllServicesStatus_ReauthAfter401(t *testing.T) {
	authCalls := 0
	statusCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		authCalls++
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		statusCalls++
		if statusCalls == 1 {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		_, _ = w.Write([]byte(objectStatusResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	// First call gets 401; client clears token.
	if _, err := c.GetAllServicesStatus(context.Background()); err == nil {
		t.Fatalf("expected error on first call")
	}

	// Second call should re-authenticate and succeed.
	if _, err := c.GetAllServicesStatus(context.Background()); err != nil {
		t.Fatalf("unexpected err on retry: %v", err)
	}

	if authCalls != 2 {
		t.Errorf("authCalls=%d, want 2 (re-auth after 401)", authCalls)
	}
}

func TestGetAllServicesStatus_MissingCredentials(t *testing.T) {
	c := NewWithBaseURL("https://localhost:7071", map[string]string{})

	_, err := c.GetAllServicesStatus(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "missing zimbra_ldap_user") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseStatusResponse_BadJSON(t *testing.T) {
	_, err := parseStatusResponse([]byte("not json"))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestParseStatusResponse_Empty(t *testing.T) {
	_, err := parseStatusResponse([]byte("{}"))
	if err == nil {
		t.Fatalf("expected error for empty response")
	}
}

func TestParseAuthResponse_NoToken(t *testing.T) {
	noToken := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body><AuthResponse xmlns="urn:zimbraAdmin" lifetime="100"></AuthResponse></soap:Body>
</soap:Envelope>`

	_, _, err := parseAuthResponse([]byte(noToken))
	if err == nil {
		t.Fatalf("expected error for empty token")
	}
}

func TestBuildAuthEnvelope_EscapesSpecialChars(t *testing.T) {
	got := buildAuthEnvelope("user", `p<a&s'd"`)
	if !strings.Contains(got, "p&lt;a&amp;s") {
		t.Errorf("password not escaped: %s", got)
	}
}

func TestNew_DefaultsToLocalAdminPort(t *testing.T) {
	c := New(map[string]string{
		"zimbra_ldap_user":     "zimbra",
		"zimbra_ldap_password": "x",
	})

	if c.baseURL != "https://localhost:7071" {
		t.Errorf("baseURL=%q, want https://localhost:7071", c.baseURL)
	}
}

// --- Transport error tests ---

func TestAuthenticate_TransportError(t *testing.T) {
	// Create a client with a custom transport that always fails.
	c := NewWithBaseURL("https://localhost:7071", map[string]string{
		"zimbra_ldap_user":     "zimbra",
		"zimbra_ldap_password": "secret",
	})

	// Replace transport with one that returns an error.
	c.httpClient.Transport = &errorRoundTripper{err: "boom"}

	_, err := c.GetAllServicesStatus(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "boom") && !strings.Contains(err.Error(), "auth request failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetAllServicesStatus_TransportErrorAfterAuth(t *testing.T) {
	// First, set up a server that returns a good auth response.
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodAuthResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	// Ensure token is cached by calling ensureToken.
	if err := c.ensureToken(context.Background()); err != nil {
		t.Fatalf("ensureToken failed: %v", err)
	}

	// Now replace the transport with one that fails.
	c.httpClient.Transport = &errorRoundTripper{err: "transport boom"}

	_, err := c.GetAllServicesStatus(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "transport boom") && !strings.Contains(err.Error(), "status request failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// errorRoundTripper is a custom http.RoundTripper that always returns an error.
type errorRoundTripper struct {
	err string
}

func (e *errorRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("%s", e.err)
}

// --- parseAuthResponse tests ---

func TestParseAuthResponse_MalformedXML(t *testing.T) {
	_, _, err := parseAuthResponse([]byte("<?xml not well-formed"))
	if err == nil {
		t.Fatalf("expected error for malformed XML")
	}

	if !strings.Contains(err.Error(), "parse auth response") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseAuthResponse_SOAPFaultWithReasonText(t *testing.T) {
	faultWithReason := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <soap:Fault>
      <faultcode>soap:Client</faultcode>
      <Reason><Text>auth denied</Text></Reason>
    </soap:Fault>
  </soap:Body>
</soap:Envelope>`

	_, _, err := parseAuthResponse([]byte(faultWithReason))
	if err == nil {
		t.Fatalf("expected error for SOAP fault")
	}

	if !strings.Contains(err.Error(), "auth denied") {
		t.Errorf("expected 'auth denied' in error, got: %v", err)
	}
}

func TestParseAuthResponse_SOAPFaultWithFaultString(t *testing.T) {
	faultWithString := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <soap:Fault>
      <faultcode>soap:Client</faultcode>
      <faultstring>bad creds</faultstring>
      <Reason><Text></Text></Reason>
    </soap:Fault>
  </soap:Body>
</soap:Envelope>`

	_, _, err := parseAuthResponse([]byte(faultWithString))
	if err == nil {
		t.Fatalf("expected error for SOAP fault")
	}

	if !strings.Contains(err.Error(), "bad creds") {
		t.Errorf("expected 'bad creds' in error, got: %v", err)
	}
}

func TestAuthenticate_LifetimeZeroFallback(t *testing.T) {
	// Server returns auth response with no lifetime.
	noLifetimeResponse := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <AuthResponse xmlns="urn:zimbraAdmin">
      <authToken>test-token</authToken>
    </AuthResponse>
  </soap:Body>
</soap:Envelope>`

	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(noLifetimeResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(objectStatusResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	_, err := c.GetAllServicesStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that expiresAt is roughly now + 1h - renewSkew.
	now := time.Now()
	expectedMin := now.Add(1*time.Hour - renewSkew - 5*time.Minute)
	expectedMax := now.Add(1*time.Hour - renewSkew + 5*time.Minute)

	if c.expiresAt.Before(expectedMin) || c.expiresAt.After(expectedMax) {
		t.Errorf("expiresAt=%v, expected roughly %v (±5min)", c.expiresAt, now.Add(1*time.Hour-renewSkew))
	}
}

func TestGetAllServicesStatus_HTTP500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	_, err := c.GetAllServicesStatus(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "HTTP 500") && !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestGetAllServicesStatus_HTTP404 pins the sentinel: mailboxd answers 404
// with an HTML error page when the Advanced extension registered no handler.
// Callers must be able to recognise that without the page leaking into output.
func TestGetAllServicesStatus_HTTP404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><head><title>Error 404</title></head></html>"))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	_, err := newTestClient(t, srv).GetAllServicesStatus(context.Background())
	if !errors.Is(err, ErrAdvancedNotRunning) {
		t.Fatalf("err = %v, want ErrAdvancedNotRunning", err)
	}

	if strings.Contains(err.Error(), "html") {
		t.Errorf("error must not carry the HTML body: %v", err)
	}
}

func TestMapToModules_Defaults(t *testing.T) {
	// Test with minimal JSON: no commandName or commercialName fields.
	minimalJSON := `{"ZxFoo":{"running":true}}`

	mods, err := parseStatusResponse([]byte(minimalJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mods) != 1 {
		t.Fatalf("expected 1 module, got %d", len(mods))
	}

	if mods[0].Name != "ZxFoo" {
		t.Errorf("Name=%q, want ZxFoo", mods[0].Name)
	}

	if mods[0].CommercialName != "ZxFoo" {
		t.Errorf("CommercialName=%q, want ZxFoo", mods[0].CommercialName)
	}

	if !mods[0].Running {
		t.Errorf("Running=%v, want true", mods[0].Running)
	}
}

// --- Retry tests -------------------------------------------------------

// TestGetAllServicesStatus_RetryOn5xxThenSuccess pins the bounded-retry
// contract: a transient 5xx from the status endpoint is retried and a
// later success is returned to the caller instead of the error.
func TestGetAllServicesStatus_RetryOn5xxThenSuccess(t *testing.T) {
	statusCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		statusCalls++
		if statusCalls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}
		_, _ = w.Write([]byte(objectStatusResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	mods, err := c.GetAllServicesStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}

	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2", len(mods))
	}

	if statusCalls != 3 {
		t.Errorf("statusCalls=%d, want 3 (2 transient failures + 1 success)", statusCalls)
	}
}

// TestGetAllServicesStatus_NoRetryOn401 pins the bounded-retry contract from
// the other side: a 401 is a request/auth problem, not a transient failure,
// so it must be returned immediately without consuming a retry attempt.
func TestGetAllServicesStatus_NoRetryOn401(t *testing.T) {
	statusCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(statusPath, func(w http.ResponseWriter, _ *http.Request) {
		statusCalls++
		w.WriteHeader(http.StatusUnauthorized)
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)

	if _, err := c.GetAllServicesStatus(context.Background()); err == nil {
		t.Fatalf("expected error, got nil")
	}

	if statusCalls != 1 {
		t.Errorf("statusCalls=%d, want 1 (401 must not be retried)", statusCalls)
	}
}
