// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// galSyncTimeout bounds every GAL sync request. CreateGalSyncAccount creates
// the account, its mailbox and the GAL data source on a mailboxd that has just
// started, which takes far longer than defaultTimeout; the legacy zmgsautil had
// no limit at all.
const galSyncTimeout = 5 * time.Minute

// The admin SOAP requests of the legacy GalSyncAccountUtil (zmgsautil).
const (
	opCreateGalSyncAccount = "CreateGalSyncAccount"
	opAddGalSyncDataSource = "AddGalSyncDataSource"
	opDeleteGalSyncAccount = "DeleteGalSyncAccount"
	opSyncGalAccount       = "SyncGalAccount"
	soapEnvelopeOpen       = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>`
	soapEnvelopeClose       = `</soap:Body></soap:Envelope>`
	pollingIntervalAttrName = "zimbraDataSourcePollingInterval"
	attrName                = "name"
	attrBy                  = "by"
)

// GalSyncAccountRequest is the CreateGalSyncAccountRequest of the admin SOAP
// API (the arguments of the legacy "zmgsautil createAccount"), and, without a
// Server, the AddGalSyncDataSourceRequest ("zmgsautil addDataSource").
type GalSyncAccountRequest struct {
	// Account is the name of the GAL sync account (-a).
	Account string
	// Name is the name of its data source (-n).
	Name string
	// Domain is the domain whose GAL it synchronises (--domain).
	Domain string
	// Type is "zimbra" or "ldap" (-t).
	Type string
	// Folder is the data source folder; omitted when empty (-f).
	Folder string
	// Server is the mailbox server that hosts the account (-s); only
	// CreateGalSyncAccount sends it.
	Server string
	// PollingInterval is zimbraDataSourcePollingInterval; omitted when
	// empty (-p).
	PollingInterval string
}

// GalSyncAccount is an account a GAL sync response reports.
type GalSyncAccount struct {
	Name string
	ID   string
}

// GalSyncRequest is the SyncGalAccountRequest ("zmgsautil trickleSync",
// "fullSync" and "forceSync"): the data source of the account to synchronise,
// by id or, when DataSourceID is empty, by name.
type GalSyncRequest struct {
	AccountID      string
	DataSourceID   string
	DataSourceName string
	// Full asks for a full synchronisation (fullSync="TRUE"); Reset deletes the
	// synchronised data first (reset="TRUE", forceSync).
	Full  bool
	Reset bool
}

// Fault is a SOAP fault of the admin service: Reason is the message of the
// Java ServiceException, Code its error code ("account.NO_SUCH_ACCOUNT").
type Fault struct {
	Op     string
	Code   string
	Reason string
}

func (f *Fault) Error() string {
	if f.Code != "" {
		return fmt.Sprintf("zxadmin: %s fault %s: %s", f.Op, f.Code, f.Reason)
	}

	return fmt.Sprintf("zxadmin: %s fault: %s", f.Op, f.Reason)
}

// xmlAttr is one attribute of an element being built; empty values are left
// out.
type xmlAttr struct{ name, value string }

// openElement writes <name xmlns="urn:zimbraAdmin"? attrs...>, the namespace
// only on the root of the request.
func openElement(b *strings.Builder, name string, root bool, attrs ...xmlAttr) {
	b.WriteString("<" + name)

	if root {
		b.WriteString(` xmlns="urn:zimbraAdmin"`)
	}

	for _, a := range attrs {
		if a.value == "" {
			continue
		}

		b.WriteString(` ` + a.name + `="`)
		xmlEscape(b, a.value)
		b.WriteString(`"`)
	}

	b.WriteString(">")
}

// textElement writes <name attrs...>text</name>.
func textElement(b *strings.Builder, name, text string, attrs ...xmlAttr) {
	openElement(b, name, false, attrs...)
	xmlEscape(b, text)
	b.WriteString("</" + name + ">")
}

// buildGalSyncAccountEnvelope is the request GalSyncAccountUtil sends for
// CreateGalSyncAccountRequest and AddGalSyncDataSourceRequest: the data source
// name, domain, type, folder and server as attributes, the account by name and
// the polling interval as an <a> child.
func buildGalSyncAccountEnvelope(op string, r *GalSyncAccountRequest) string {
	var b strings.Builder

	b.WriteString(soapEnvelopeOpen)
	openElement(&b, op+"Request", true,
		xmlAttr{attrName, r.Name}, xmlAttr{"domain", r.Domain}, xmlAttr{"type", r.Type},
		xmlAttr{"folder", r.Folder}, xmlAttr{"server", r.Server})
	textElement(&b, "account", r.Account, xmlAttr{attrBy, attrName})

	if r.PollingInterval != "" {
		textElement(&b, "a", r.PollingInterval, xmlAttr{"n", pollingIntervalAttrName})
	}

	b.WriteString("</" + op + "Request>" + soapEnvelopeClose)

	return b.String()
}

// buildSyncGalEnvelope is the SyncGalAccountRequest: the account by id with its
// data source by id or name.
func buildSyncGalEnvelope(r *GalSyncRequest) string {
	by, ds := attrName, r.DataSourceName
	if r.DataSourceID != "" {
		by, ds = "id", r.DataSourceID
	}

	attrs := []xmlAttr{{attrBy, by}}
	if r.Full {
		attrs = append(attrs, xmlAttr{"fullSync", "TRUE"})
	}

	if r.Reset {
		attrs = append(attrs, xmlAttr{"reset", "TRUE"})
	}

	var b strings.Builder

	b.WriteString(soapEnvelopeOpen)
	openElement(&b, opSyncGalAccount+"Request", true)
	openElement(&b, "account", false, xmlAttr{"id", r.AccountID})
	textElement(&b, "datasource", ds, attrs...)
	b.WriteString("</account></" + opSyncGalAccount + "Request>" + soapEnvelopeClose)

	return b.String()
}

// buildDeleteGalSyncEnvelope is the DeleteGalSyncAccountRequest: the account
// by name, or by id when byID is set.
func buildDeleteGalSyncEnvelope(account string, byID bool) string {
	by := attrName
	if byID {
		by = "id"
	}

	var b strings.Builder

	b.WriteString(soapEnvelopeOpen)
	openElement(&b, opDeleteGalSyncAccount+"Request", true)
	textElement(&b, "account", account, xmlAttr{attrBy, by})
	b.WriteString("</" + opDeleteGalSyncAccount + "Request>" + soapEnvelopeClose)

	return b.String()
}

// galSyncEnvelope decodes the answer: the accounts of the response (whatever
// its name), or a SOAP fault (the server sends a fault with HTTP 500).
type galSyncEnvelope struct {
	Body struct {
		Response struct {
			Accounts []struct {
				Name string `xml:"name,attr"`
				ID   string `xml:"id,attr"`
			} `xml:"account"`
		} `xml:",any"`
		Fault *struct {
			FaultString string `xml:"faultstring"`
			Reason      struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
			Detail struct {
				Error struct {
					Code string `xml:"Code"`
				} `xml:"Error"`
			} `xml:"Detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

// callGalSync sends one GAL sync request: admin authentication with the LDAP
// master credentials, then the envelope. The request is sent once and is not
// retried: a retry after a timeout could meet the account the first attempt
// created. A SOAP fault is returned as a *Fault.
func (c *Client) callGalSync(ctx context.Context, op, envelope string) ([]GalSyncAccount, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "zxadmin")

	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, galSyncTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+soapPath, strings.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("zxadmin: build %s request: %w", op, err)
	}

	req.Header.Set("Content-Type", "text/xml; charset=UTF-8")
	req.AddCookie(authCookie(token))

	// ctx is the deadline of this call, not the client's short timeout.
	hc := *c.httpClient
	hc.Timeout = 0

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zxadmin: %s request failed: %w", op, err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("zxadmin: read %s response: %w", op, err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.dropToken(token)

		return nil, fmt.Errorf("zxadmin: %s auth rejected: HTTP %d", op, resp.StatusCode)
	}

	return parseGalSyncReply(op, resp.StatusCode, raw)
}

func parseGalSyncReply(op string, status int, raw []byte) ([]GalSyncAccount, error) {
	var env galSyncEnvelope

	if err := xml.Unmarshal(raw, &env); err != nil {
		if status != http.StatusOK {
			return nil, fmt.Errorf("zxadmin: %s failed: HTTP %d: %s", op, status, truncate(string(raw)))
		}

		return nil, fmt.Errorf("zxadmin: parse %s response: %w", op, err)
	}

	if f := env.Body.Fault; f != nil {
		reason := f.Reason.Text
		if reason == "" {
			reason = f.FaultString
		}

		return nil, &Fault{Op: op, Code: f.Detail.Error.Code, Reason: reason}
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("zxadmin: %s failed: HTTP %d: %s", op, status, truncate(string(raw)))
	}

	accounts := make([]GalSyncAccount, 0, len(env.Body.Response.Accounts))
	for _, a := range env.Body.Response.Accounts {
		accounts = append(accounts, GalSyncAccount{Name: a.Name, ID: a.ID})
	}

	return accounts, nil
}

// CreateGalSyncAccount creates a GAL sync account like the legacy
// "zmgsautil createAccount" and returns the account of the response.
func (c *Client) CreateGalSyncAccount(ctx context.Context, r *GalSyncAccountRequest) (GalSyncAccount, error) {
	accounts, err := c.callGalSync(ctx, opCreateGalSyncAccount,
		buildGalSyncAccountEnvelope(opCreateGalSyncAccount, r))
	if err != nil {
		return GalSyncAccount{}, err
	}

	if len(accounts) == 0 || accounts[0].Name == "" || accounts[0].ID == "" {
		return GalSyncAccount{}, errors.New("zxadmin: CreateGalSyncAccount response has no account")
	}

	return accounts[0], nil
}

// AddGalSyncDataSource adds a GAL data source to an account like the legacy
// "zmgsautil addDataSource" and returns the accounts of the response. The
// Server of r is not sent.
func (c *Client) AddGalSyncDataSource(ctx context.Context, r *GalSyncAccountRequest) ([]GalSyncAccount, error) {
	add := *r
	add.Server = ""

	return c.callGalSync(ctx, opAddGalSyncDataSource, buildGalSyncAccountEnvelope(opAddGalSyncDataSource, &add))
}

// DeleteGalSyncAccount deletes a GAL sync account like the legacy
// "zmgsautil deleteAccount": by id when byID is set, else by name.
func (c *Client) DeleteGalSyncAccount(ctx context.Context, account string, byID bool) error {
	_, err := c.callGalSync(ctx, opDeleteGalSyncAccount, buildDeleteGalSyncEnvelope(account, byID))

	return err
}

// SyncGalAccount asks mailboxd to synchronise the data source of a GAL sync
// account like the legacy "zmgsautil trickleSync", "fullSync" and "forceSync".
func (c *Client) SyncGalAccount(ctx context.Context, r *GalSyncRequest) error {
	_, err := c.callGalSync(ctx, opSyncGalAccount, buildSyncGalEnvelope(r))

	return err
}
