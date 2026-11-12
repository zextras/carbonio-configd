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

// galSyncTimeout bounds CreateGalSyncAccount. The call creates the account,
// its mailbox and the GAL data source on a mailboxd that has just started,
// which takes far longer than defaultTimeout; the legacy zmgsautil had no
// limit at all.
const galSyncTimeout = 5 * time.Minute

// GalSyncAccountRequest is the CreateGalSyncAccountRequest of the admin SOAP
// API (the arguments of the legacy "zmgsautil createAccount").
type GalSyncAccountRequest struct {
	// Account is the name of the GAL sync account to create (-a).
	Account string
	// Name is the name of its data source (-n).
	Name string
	// Domain is the domain whose GAL it synchronises (--domain).
	Domain string
	// Type is "zimbra" or "ldap" (-t).
	Type string
	// Folder is the data source folder; omitted when empty (-f).
	Folder string
	// Server is the mailbox server that hosts the account (-s).
	Server string
	// PollingInterval is zimbraDataSourcePollingInterval; omitted when
	// empty (-p).
	PollingInterval string
}

// GalSyncAccount is the account CreateGalSyncAccountResponse reports.
type GalSyncAccount struct {
	Name string
	ID   string
}

// buildCreateGalSyncAccountEnvelope is the request GalSyncAccountUtil sends:
// the data source name, domain, type, folder and server as attributes, the
// account by name and the polling interval as an <a> child.
func buildCreateGalSyncAccountEnvelope(r *GalSyncAccountRequest) string {
	var b strings.Builder

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>`)
	b.WriteString(`<CreateGalSyncAccountRequest xmlns="urn:zimbraAdmin"`)

	attrs := []struct{ name, value string }{
		{"name", r.Name}, {"domain", r.Domain}, {"type", r.Type}, {"folder", r.Folder}, {"server", r.Server},
	}
	for _, a := range attrs {
		if a.value == "" {
			continue
		}

		b.WriteString(` ` + a.name + `="`)
		xmlEscape(&b, a.value)
		b.WriteString(`"`)
	}

	b.WriteString(`><account by="name">`)
	xmlEscape(&b, r.Account)
	b.WriteString(`</account>`)

	if r.PollingInterval != "" {
		b.WriteString(`<a n="zimbraDataSourcePollingInterval">`)
		xmlEscape(&b, r.PollingInterval)
		b.WriteString(`</a>`)
	}

	b.WriteString(`</CreateGalSyncAccountRequest></soap:Body></soap:Envelope>`)

	return b.String()
}

// galSyncEnvelope decodes the answer: the new account, or a SOAP fault (the
// server sends a fault with HTTP 500).
type galSyncEnvelope struct {
	Body struct {
		Response struct {
			Account struct {
				Name string `xml:"name,attr"`
				ID   string `xml:"id,attr"`
			} `xml:"account"`
		} `xml:"CreateGalSyncAccountResponse"`
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

// CreateGalSyncAccount creates a GAL sync account like the legacy
// "zmgsautil createAccount": admin authentication with the LDAP master
// credentials, then CreateGalSyncAccountRequest. The request is sent once and
// is not retried: a retry after a timeout could meet the account the first
// attempt created. A SOAP fault is returned as an error with its code and
// reason.
func (c *Client) CreateGalSyncAccount(ctx context.Context, r *GalSyncAccountRequest) (GalSyncAccount, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "zxadmin")

	token, err := c.ensureToken(ctx)
	if err != nil {
		return GalSyncAccount{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, galSyncTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+soapPath,
		strings.NewReader(buildCreateGalSyncAccountEnvelope(r)))
	if err != nil {
		return GalSyncAccount{}, fmt.Errorf("zxadmin: build CreateGalSyncAccount request: %w", err)
	}

	req.Header.Set("Content-Type", "text/xml; charset=UTF-8")
	req.AddCookie(authCookie(token))

	// ctx is the deadline of this call, not the client's short timeout.
	hc := *c.httpClient
	hc.Timeout = 0

	resp, err := hc.Do(req)
	if err != nil {
		return GalSyncAccount{}, fmt.Errorf("zxadmin: CreateGalSyncAccount request failed: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return GalSyncAccount{}, fmt.Errorf("zxadmin: read CreateGalSyncAccount response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.dropToken(token)

		return GalSyncAccount{}, fmt.Errorf("zxadmin: CreateGalSyncAccount auth rejected: HTTP %d", resp.StatusCode)
	}

	return parseCreateGalSyncAccount(resp.StatusCode, raw)
}

func parseCreateGalSyncAccount(status int, raw []byte) (GalSyncAccount, error) {
	var env galSyncEnvelope

	if err := xml.Unmarshal(raw, &env); err != nil {
		if status != http.StatusOK {
			return GalSyncAccount{}, fmt.Errorf("zxadmin: CreateGalSyncAccount failed: HTTP %d: %s",
				status, truncate(string(raw)))
		}

		return GalSyncAccount{}, fmt.Errorf("zxadmin: parse CreateGalSyncAccount response: %w", err)
	}

	if f := env.Body.Fault; f != nil {
		reason := f.Reason.Text
		if reason == "" {
			reason = f.FaultString
		}

		if code := f.Detail.Error.Code; code != "" {
			return GalSyncAccount{}, fmt.Errorf("zxadmin: CreateGalSyncAccount fault %s: %s", code, reason)
		}

		return GalSyncAccount{}, fmt.Errorf("zxadmin: CreateGalSyncAccount fault: %s", reason)
	}

	if status != http.StatusOK {
		return GalSyncAccount{}, fmt.Errorf("zxadmin: CreateGalSyncAccount failed: HTTP %d: %s",
			status, truncate(string(raw)))
	}

	acct := env.Body.Response.Account
	if acct.Name == "" || acct.ID == "" {
		return GalSyncAccount{}, errors.New("zxadmin: CreateGalSyncAccount response has no account")
	}

	return GalSyncAccount{Name: acct.Name, ID: acct.ID}, nil
}
