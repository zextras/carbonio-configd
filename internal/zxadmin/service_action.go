// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zxadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// ServiceAction is an action of the Advanced service-switch handler.
type ServiceAction string

// The service actions. The values are the action names of the endpoint.
const (
	ServiceStart   ServiceAction = "doStartService"
	ServiceStop    ServiceAction = "doStopService"
	ServiceRestart ServiceAction = "doRestartService"
)

// ModuleService is the service switch every Advanced module registers for the
// module as a whole.
const ModuleService = "module"

// actionTimeout bounds one service action. A module start can run for minutes
// (the default client timeout is meant for status probes).
const actionTimeout = 5 * time.Minute

// ServiceError is the failure the Advanced handler reports for an action: the
// request reached the module and was refused or failed. Message is the
// server's message with its {placeholders} filled in from the error details.
type ServiceError struct {
	Code    string
	Message string
}

func (e *ServiceError) Error() string {
	if e.Message == "" {
		return e.Code
	}

	return e.Message
}

// serviceReply is the JSON answer of a service action. A module-level failure
// arrives under "error", an unknown module under "exception".
type serviceReply struct {
	OK       bool `json:"ok"`
	Response struct {
		Message string `json:"message"`
	} `json:"response"`
	Error     *replyError `json:"error"`
	Exception *replyError `json:"exception"`
}

type replyError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// render fills the {key} placeholders of the message with the string values
// of the details; other placeholders are left as they are.
func (e *replyError) render() *ServiceError {
	msg := e.Message

	for key, v := range e.Details {
		if s, ok := v.(string); ok {
			msg = strings.ReplaceAll(msg, "{"+key+"}", s)
		}
	}

	return &ServiceError{Code: e.Code, Message: msg}
}

// ControlService runs action on service of the Advanced module moduleID (for
// example "ZxBackup", service ModuleService) through the running mailboxd and
// returns the server's confirmation message. A refusal is a *ServiceError and
// a missing Advanced handler is ErrAdvancedNotRunning.
//
// The action request is sent once and is never retried: a retried restart
// would restart the module twice.
func (c *Client) ControlService(
	ctx context.Context, moduleID string, action ServiceAction, service string,
) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "zxadmin")

	token, err := c.ensureToken(ctx)
	if err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("module", moduleID)
	q.Set("action", string(action))
	q.Set("service_name", service)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+statusPath+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return "", fmt.Errorf("zxadmin: build %s request: %w", action, err)
	}

	req.AddCookie(authCookie(token))

	// Same transport (TLS policy, connection reuse), longer overall bound.
	slow := *c.httpClient
	slow.Timeout = actionTimeout

	resp, err := slow.Do(req)
	if err != nil {
		logger.WarnContext(ctx, "zxadmin: service action request failed", "action", action, "error", err)

		return "", fmt.Errorf("zxadmin: %s request failed: %w", action, err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("zxadmin: read %s response: %w", action, err)
	}

	return c.parseServiceReply(ctx, token, action, resp.StatusCode, raw)
}

func (c *Client) parseServiceReply(
	ctx context.Context, token string, action ServiceAction, status int, raw []byte,
) (string, error) {
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		c.dropToken(token)

		logger.WarnContext(ctx, "zxadmin: service action auth rejected, dropping cached token", "status", status)

		return "", fmt.Errorf("zxadmin: %s auth rejected: HTTP %d", action, status)
	case http.StatusNotFound:
		logger.WarnContext(ctx, "zxadmin: advanced extension handler not registered (HTTP 404)")

		return "", ErrAdvancedNotRunning
	default:
		return "", fmt.Errorf("zxadmin: %s failed: HTTP %d: %s", action, status, truncate(string(raw)))
	}

	var reply serviceReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("zxadmin: parse %s response: %w (body=%s)", action, err, truncate(string(raw)))
	}

	if reply.OK {
		return reply.Response.Message, nil
	}

	switch {
	case reply.Error != nil:
		return "", reply.Error.render()
	case reply.Exception != nil:
		return "", reply.Exception.render()
	default:
		return "", errors.New("zxadmin: " + string(action) + " refused without a reason: " + truncate(string(raw)))
	}
}
