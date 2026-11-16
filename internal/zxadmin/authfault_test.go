// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func soapFaultBody(code string) string {
	return `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
		`<soap:Fault><soap:Reason><soap:Text>fault text</soap:Text></soap:Reason>` +
		`<soap:Detail><Error xmlns="urn:zimbra"><Code>` + code + `</Code></Error></soap:Detail>` +
		`</soap:Fault></soap:Body></soap:Envelope>`
}

// faultServer answers the admin auth, and any other request on path with a
// fault (HTTP 500) for the first `faults` requests, then with ok.
type faultServer struct {
	*httptest.Server
	auths, requests atomic.Int32
}

func newFaultServer(t *testing.T, path, code string, faults int32, ok string) *faultServer {
	t.Helper()

	fs := &faultServer{}
	mux := http.NewServeMux()

	handle := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "AuthRequest") {
			fs.auths.Add(1)
			_, _ = w.Write([]byte(goodAuthResponse))

			return
		}

		if fs.requests.Add(1) <= faults {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(soapFaultBody(code)))

			return
		}

		_, _ = w.Write([]byte(ok))
	}

	mux.HandleFunc(soapPath, handle)

	if path != soapPath {
		mux.HandleFunc(path, handle)
	}

	fs.Server = httptest.NewTLSServer(mux)
	t.Cleanup(fs.Close)

	return fs
}

const reloadOK = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
	`<ReloadLocalConfigResponse xmlns="urn:zimbraAdmin"/></soap:Body></soap:Envelope>`

// A token the server invalidated before our own expiry comes back as a SOAP
// fault with HTTP 500: the token is dropped and the call repeats once with a
// fresh one.
func TestAuthFaultOn500DropsTokenAndReauthenticatesOnce(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"service.AUTH_EXPIRED", "service.AUTH_REQUIRED"} {
		fs := newFaultServer(t, soapPath, code, 1, reloadOK)

		require.NoError(t, newTestClient(t, fs.Server).ReloadLocalConfig(context.Background()), code)
		assert.EqualValues(t, 2, fs.auths.Load(), code)
		assert.EqualValues(t, 2, fs.requests.Load(), code)
	}
}

func TestAuthFaultTwiceIsReturnedAndTheTokenDropped(t *testing.T) {
	t.Parallel()

	fs := newFaultServer(t, soapPath, "service.AUTH_EXPIRED", 10, reloadOK)
	c := newTestClient(t, fs.Server)

	err := c.ReloadLocalConfig(context.Background())

	var fault *Fault

	require.ErrorAs(t, err, &fault)
	assert.Equal(t, "service.AUTH_EXPIRED", fault.Code)
	assert.EqualValues(t, 2, fs.requests.Load(), "one re-authentication, not a loop")
	assert.Empty(t, c.token)
}

// Any other fault is the server's answer to the request, not a transient
// failure: one attempt, token kept.
func TestOtherFaultOn500IsNotRetried(t *testing.T) {
	t.Parallel()

	fs := newFaultServer(t, soapPath, "service.FAILURE", 10, reloadOK)
	c := newTestClient(t, fs.Server)

	err := c.ReloadLocalConfig(context.Background())

	var fault *Fault

	require.ErrorAs(t, err, &fault)
	assert.Equal(t, "service.FAILURE", fault.Code)
	assert.EqualValues(t, 1, fs.requests.Load())
	assert.EqualValues(t, 1, fs.auths.Load())
	assert.NotEmpty(t, c.token)
}

func TestAuthFaultOnStatusReauthenticates(t *testing.T) {
	t.Parallel()

	fs := newFaultServer(t, statusPath, "service.AUTH_EXPIRED", 1, objectStatusResponse)

	mods, err := newTestClient(t, fs.Server).GetAllServicesStatus(context.Background())
	require.NoError(t, err)
	assert.Len(t, mods, 2)
	assert.EqualValues(t, 2, fs.requests.Load(), "the fault is not retried as a 5xx, the re-authentication is")
}

func TestAuthFaultOnServiceActionReauthenticates(t *testing.T) {
	t.Parallel()

	fs := newFaultServer(t, statusPath, "service.AUTH_REQUIRED", 1, `{"response":{"message":"done"},"ok":true}`)

	msg, err := newTestClient(t, fs.Server).ControlService(context.Background(), "ZxBackup", ServiceRestart, ModuleService)
	require.NoError(t, err)
	assert.Equal(t, "done", msg)
	assert.EqualValues(t, 2, fs.auths.Load())
}

func TestAuthFaultOnGalSyncReauthenticates(t *testing.T) {
	t.Parallel()

	fs := newFaultServer(t, soapPath, "service.AUTH_EXPIRED", 1, galSyncOK)

	_, err := newTestClient(t, fs.Server).CreateGalSyncAccount(context.Background(), &GalSyncAccountRequest{})
	require.NoError(t, err)
	assert.EqualValues(t, 2, fs.auths.Load())
	assert.EqualValues(t, 2, fs.requests.Load())
}
