// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"crypto/tls"
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
