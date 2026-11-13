// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// galSyncRequest decodes what CreateGalSyncAccount sends.
type galSyncRequest struct {
	Body struct {
		Request struct {
			XMLName xml.Name
			Name    string `xml:"name,attr"`
			Domain  string `xml:"domain,attr"`
			Type    string `xml:"type,attr"`
			Folder  string `xml:"folder,attr"`
			Server  string `xml:"server,attr"`
			Account struct {
				By   string `xml:"by,attr"`
				Name string `xml:",chardata"`
			} `xml:"account"`
			Attrs []struct {
				Name  string `xml:"n,attr"`
				Value string `xml:",chardata"`
			} `xml:"a"`
		} `xml:"CreateGalSyncAccountRequest"`
	} `xml:"Body"`
}

const galSyncOK = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
	`<CreateGalSyncAccountResponse xmlns="urn:zimbraAdmin">` +
	`<account name="galsync.abc@example.com" id="0b1c2d3e-0000-4000-8000-000000000001"/>` +
	`</CreateGalSyncAccountResponse></soap:Body></soap:Envelope>`

const galSyncFault = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
	`<soap:Fault><soap:Code><soap:Value>soap:Sender</soap:Value></soap:Code>` +
	`<soap:Reason><soap:Text>no such domain: example.com</soap:Text></soap:Reason>` +
	`<soap:Detail><Error xmlns="urn:zimbra"><Code>account.NO_SUCH_DOMAIN</Code></Error></soap:Detail>` +
	`</soap:Fault></soap:Body></soap:Envelope>`

// galSyncServer answers the admin auth and hands every other request to
// handle, counting those.
func galSyncServer(t *testing.T, calls *atomic.Int32, handle http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "AuthRequest") {
			_, _ = w.Write([]byte(goodAuthResponse))

			return
		}

		calls.Add(1)
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		handle(w, r)
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func testGalSyncRequest() *GalSyncAccountRequest {
	return &GalSyncAccountRequest{
		Account: "galsync.abc@example.com", Name: "InternalGAL", Domain: "example.com", Type: "zimbra",
		Folder: "_InternalGAL", Server: "store.example.com", PollingInterval: "1d",
	}
}

func TestCreateGalSyncAccount_SendsTheRequestOfGalSyncAccountUtil(t *testing.T) {
	var (
		calls  atomic.Int32
		got    galSyncRequest
		cookie string
	)

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		assert.NoError(t, xml.Unmarshal(raw, &got))

		if ck, err := r.Cookie(authCookieName); err == nil {
			cookie = ck.Value
		}

		_, _ = w.Write([]byte(galSyncOK))
	})

	acct, err := newTestClient(t, srv).CreateGalSyncAccount(context.Background(), testGalSyncRequest())
	require.NoError(t, err)

	assert.Equal(t, GalSyncAccount{Name: "galsync.abc@example.com", ID: "0b1c2d3e-0000-4000-8000-000000000001"}, acct)
	assert.Equal(t, "fake-token-xyz", cookie, "the call is made with the token of the auth")

	r := got.Body.Request
	assert.Equal(t, xml.Name{Space: "urn:zimbraAdmin", Local: "CreateGalSyncAccountRequest"}, r.XMLName)
	assert.Equal(t, "InternalGAL", r.Name)
	assert.Equal(t, "example.com", r.Domain)
	assert.Equal(t, "zimbra", r.Type)
	assert.Equal(t, "_InternalGAL", r.Folder)
	assert.Equal(t, "store.example.com", r.Server)
	assert.Equal(t, "name", r.Account.By)
	assert.Equal(t, "galsync.abc@example.com", r.Account.Name)
	require.Len(t, r.Attrs, 1)
	assert.Equal(t, "zimbraDataSourcePollingInterval", r.Attrs[0].Name)
	assert.Equal(t, "1d", r.Attrs[0].Value)
}

func TestCreateGalSyncAccount_OptionalPartsAreOmittedAndValuesEscaped(t *testing.T) {
	body := buildGalSyncAccountEnvelope(opCreateGalSyncAccount, &GalSyncAccountRequest{
		Account: "galsync.a&b@example.com", Name: `In"ternal`, Domain: "example.com", Type: "zimbra", Server: "s",
	})

	assert.NotContains(t, body, "folder=")
	assert.NotContains(t, body, "zimbraDataSourcePollingInterval")

	var got galSyncRequest

	require.NoError(t, xml.Unmarshal([]byte(body), &got), "the request is well-formed XML")
	assert.Equal(t, `In"ternal`, got.Body.Request.Name)
	assert.Equal(t, "galsync.a&b@example.com", got.Body.Request.Account.Name)
}

func TestCreateGalSyncAccount_FaultIsAnErrorWithCodeAndReasonAndIsNotRetried(t *testing.T) {
	var calls atomic.Int32

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(galSyncFault))
	})

	_, err := newTestClient(t, srv).CreateGalSyncAccount(context.Background(), testGalSyncRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account.NO_SUCH_DOMAIN")
	assert.Contains(t, err.Error(), "no such domain: example.com")
	assert.EqualValues(t, 1, calls.Load(), "a retry could meet the account the first attempt created")
}

func TestCreateGalSyncAccount_FaultWithoutDetailCodeKeepsTheReason(t *testing.T) {
	_, err := parseGalSyncReply(opCreateGalSyncAccount, http.StatusInternalServerError, []byte(
		`<Envelope><Body><Fault><faultstring>boom</faultstring></Fault></Body></Envelope>`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestCreateGalSyncAccount_FailuresWithoutAFault(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"html error page", http.StatusBadGateway, "<html>bad gateway</html>", "HTTP 502"},
		{"not xml on 200", http.StatusOK, "nope", "parse CreateGalSyncAccount response"},
		{"xml error status", http.StatusBadRequest, `<Envelope><Body/></Envelope>`, "HTTP 400"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseGalSyncReply(opCreateGalSyncAccount, tc.status, []byte(tc.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCreateGalSyncAccount_AuthRejectedDropsToken(t *testing.T) {
	var calls atomic.Int32

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	c := newTestClient(t, srv)

	_, err := c.CreateGalSyncAccount(context.Background(), testGalSyncRequest())
	require.ErrorContains(t, err, "auth rejected")
	assert.Empty(t, c.token, "the rejected token is forgotten")
}

func TestCreateGalSyncAccount_AuthFailureIsReturned(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	_, err := newTestClient(t, srv).CreateGalSyncAccount(context.Background(), testGalSyncRequest())
	require.ErrorContains(t, err, "auth failed")
}

// Creating the account (and its mailbox) outlasts the client's short timeout.
func TestCreateGalSyncAccount_IsNotCutOffByTheClientTimeout(t *testing.T) {
	var calls atomic.Int32

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(galSyncOK))
	})

	c := newTestClient(t, srv)
	c.httpClient.Timeout = 50 * time.Millisecond

	// The auth is quick; only the creation is slow.
	acct, err := c.CreateGalSyncAccount(context.Background(), testGalSyncRequest())
	require.NoError(t, err)
	assert.Equal(t, "galsync.abc@example.com", acct.Name)
}

func TestCreateGalSyncAccount_ContextCancelIsReturned(t *testing.T) {
	var calls atomic.Int32

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := newTestClient(t, srv).CreateGalSyncAccount(ctx, testGalSyncRequest())
	require.Error(t, err)
}

func TestCreateGalSyncAccount_ResponseWithoutAccountIsAnError(t *testing.T) {
	var calls atomic.Int32

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<Envelope><Body><CreateGalSyncAccountResponse/></Body></Envelope>`))
	})

	_, err := newTestClient(t, srv).CreateGalSyncAccount(context.Background(), testGalSyncRequest())
	require.ErrorContains(t, err, "no account")
}

func TestFault_IsReturnedTyped(t *testing.T) {
	var calls atomic.Int32

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(galSyncFault))
	})

	err := newTestClient(t, srv).DeleteGalSyncAccount(context.Background(), "galsync@example.com", false)

	var fault *Fault

	require.ErrorAs(t, err, &fault)
	assert.Equal(t, "no such domain: example.com", fault.Reason)
	assert.Equal(t, "account.NO_SUCH_DOMAIN", fault.Code)
}

func TestAuthFault_IsReturnedTypedEvenWithHTTP500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(soapPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<Envelope><Body><Fault><Reason><Text>authentication failed for [zimbra]</Text></Reason></Fault></Body></Envelope>`))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	err := newTestClient(t, srv).SyncGalAccount(context.Background(), &GalSyncRequest{AccountID: "x", DataSourceName: "y"})

	var fault *Fault

	require.ErrorAs(t, err, &fault)
	assert.Equal(t, "authentication failed for [zimbra]", fault.Reason)
}

// sentBody runs call against a server answering with an empty success and
// returns the request the server received.
func sentBody(t *testing.T, call func(c *Client) error) string {
	t.Helper()

	var (
		calls atomic.Int32
		sent  string
	)

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = string(raw)

		_, _ = w.Write([]byte(`<Envelope><Body><Response xmlns="urn:zimbraAdmin"/></Body></Envelope>`))
	})

	require.NoError(t, call(newTestClient(t, srv)))

	return sent
}

func TestAddGalSyncDataSource_SendsNoServerAndReturnsTheAccounts(t *testing.T) {
	var (
		calls atomic.Int32
		sent  string
	)

	srv := galSyncServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = string(raw)

		_, _ = w.Write([]byte(`<Envelope><Body><AddGalSyncDataSourceResponse xmlns="urn:zimbraAdmin">` +
			`<account name="a@example.com" id="id-a"/><account name="b@example.com" id="id-b"/>` +
			`</AddGalSyncDataSourceResponse></Body></Envelope>`))
	})

	accounts, err := newTestClient(t, srv).AddGalSyncDataSource(context.Background(), testGalSyncRequest())
	require.NoError(t, err)

	assert.Equal(t, []GalSyncAccount{{"a@example.com", "id-a"}, {"b@example.com", "id-b"}}, accounts)
	assert.Contains(t, sent, "<AddGalSyncDataSourceRequest ")
	assert.NotContains(t, sent, "server=")
	assert.Contains(t, sent, `name="InternalGAL"`)
	assert.Contains(t, sent, `<a n="zimbraDataSourcePollingInterval">1d</a>`)
}

func TestDeleteGalSyncAccount_AccountByNameOrByID(t *testing.T) {
	byName := sentBody(t, func(c *Client) error { return c.DeleteGalSyncAccount(context.Background(), "g@example.com", false) })
	byID := sentBody(t, func(c *Client) error { return c.DeleteGalSyncAccount(context.Background(), "id-1", true) })

	assert.Contains(t, byName, `<DeleteGalSyncAccountRequest xmlns="urn:zimbraAdmin"><account by="name">g@example.com</account>`)
	assert.Contains(t, byID, `<account by="id">id-1</account>`)
}

func TestSyncGalAccount_DataSourceByIDOrNameWithFullAndReset(t *testing.T) {
	tests := []struct {
		name string
		req  GalSyncRequest
		want string
	}{
		{"trickle by name", GalSyncRequest{AccountID: "a1", DataSourceName: "InternalGAL"},
			`<account id="a1"><datasource by="name">InternalGAL</datasource></account>`},
		{"id wins over name", GalSyncRequest{AccountID: "a1", DataSourceID: "d1", DataSourceName: "InternalGAL"},
			`<datasource by="id">d1</datasource>`},
		{"full", GalSyncRequest{AccountID: "a1", DataSourceID: "d1", Full: true},
			`<datasource by="id" fullSync="TRUE">d1</datasource>`},
		{"force", GalSyncRequest{AccountID: "a1", DataSourceID: "d1", Full: true, Reset: true},
			`<datasource by="id" fullSync="TRUE" reset="TRUE">d1</datasource>`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sent := sentBody(t, func(c *Client) error { return c.SyncGalAccount(context.Background(), &tc.req) })

			assert.Contains(t, sent, `<SyncGalAccountRequest xmlns="urn:zimbraAdmin">`)
			assert.Contains(t, sent, tc.want)
		})
	}
}
