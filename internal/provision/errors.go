// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package provision is the native replacement of the zmprov commands the
// bootstrap runs (cs, cd, ca, aaa, mcf, ms, mc): it creates and modifies the
// directory entries over LDAP exactly as the mailbox's Java provisioning
// (ProvUtil -> LdapProvisioning) does, from the attribute schema
// (conf/attrs/*.xml) to the entry layout, the generated ids and timestamps,
// the attribute value checks and the attribute callbacks.
package provision

import (
	"errors"
	"fmt"

	goldap "github.com/go-ldap/ldap/v3"
)

// The error codes of the Java service exceptions the commands raise.
const (
	CodeInvalidRequest   = "service.INVALID_REQUEST"
	CodeInvalidAttrValue = "account.INVALID_ATTR_VALUE"
	CodeInvalidAttrName  = "account.INVALID_ATTR_NAME"
	CodeBadPassword      = "account.INVALID_PASSWORD"
	CodeNoSuchDomain     = "account.NO_SUCH_DOMAIN"
	CodeNoSuchAccount    = "account.NO_SUCH_ACCOUNT"
	CodeNoSuchServer     = "account.NO_SUCH_SERVER"
	CodeNoSuchCOS        = "account.NO_SUCH_COS"
	CodeDomainExists     = "account.DOMAIN_EXISTS"
	CodeAccountExists    = "account.ACCOUNT_EXISTS"
	CodeServerExists     = "account.SERVER_EXISTS"
	CodeFailure          = "service.FAILURE"
)

// Error is a provisioning failure with the code of the Java exception that
// zmprov would have printed, "ERROR: <code> (<message>)".
type Error struct {
	Code string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s (%s: %v)", e.Code, e.Msg, e.Err)
	}

	return fmt.Sprintf("%s (%s)", e.Code, e.Msg)
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func failWith(err error, format string, args ...any) *Error {
	return &Error{Code: CodeFailure, Msg: fmt.Sprintf(format, args...), Err: err}
}

// modifyFailed is the exception of LdapProvisioning.modifyLdapAttrs for a
// modification the directory refused. LdapException.mapToLdapException turns
// "undefined attribute type" into the invalid attr name exception and
// "constraint violation" or "invalid attribute syntax" into the invalid attr
// value one, and modifyLdapAttrs prefixes their message with its own; any
// other failure is service.FAILURE.
func modifyFailed(err error, dn string) *Error {
	if ldapErr, ok := errors.AsType[*goldap.Error](err); ok {
		switch ldapErr.ResultCode {
		case goldap.LDAPResultUndefinedAttributeType:
			return fail(CodeInvalidAttrName,
				"invalid attr name: invalid attr name - unable to modify attributes: %v", ldapErr.Err)
		case goldap.LDAPResultConstraintViolation, goldap.LDAPResultInvalidAttributeSyntax:
			return fail(CodeInvalidAttrValue,
				"invalid attr value: invalid attr value - unable to modify attributes: %v", ldapErr.Err)
		}
	}

	return failWith(err, "unable to modify %s", dn)
}

// invalidRequest is service.INVALID_REQUEST.
func invalidRequest(format string, args ...any) *Error {
	return fail(CodeInvalidRequest, format, args...)
}

// invalidValue is account.INVALID_ATTR_VALUE.
func invalidValue(format string, args ...any) *Error {
	return fail(CodeInvalidAttrValue, format, args...)
}
