// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// provKind is how provAttr.valid checks a value, mirroring the checks of
// zmprov's LdapAttributeInfo.checkValue for the attribute type.
type provKind int

const (
	// provString: any value, at most max characters (0: no limit).
	provString provKind = iota
	// provBool: TRUE or FALSE.
	provBool
	// provInt: an integer, 0 or more.
	provInt
	// provEnum: one of enum.
	provEnum
	// provDuration: digits and an optional unit h, m, s, d or ms.
	provDuration
	// provBinary: the value is the path of a file; the attribute holds its
	// content (zmprov's binary I/O).
	provBinary
)

// provAttr describes one attribute of provAttrList.
type provAttr struct {
	kind provKind
	max  int
	enum []string
}

var provDurationRE = regexp.MustCompile(`^\d+([hmsd]|ms)?$`)

// valid reports whether value would pass zmprov's checkValue. The empty value
// always does: it unsets the attribute.
func (a provAttr) valid(value string) bool {
	if value == "" {
		return true
	}

	switch a.kind {
	case provBool:
		return value == "TRUE" || value == "FALSE"
	case provInt:
		n, err := strconv.ParseInt(value, 10, 32)

		return err == nil && n >= 0
	case provEnum:
		return slices.Contains(a.enum, value)
	case provDuration:
		return provDurationRE.MatchString(value)
	case provBinary:
		return true
	default:
		return a.max == 0 || len(value) <= a.max
	}
}

var provAttrIndex = sync.OnceValue(func() map[string]provAttr {
	idx := make(map[string]provAttr, len(provAttrList))

	for name, a := range provAttrList {
		idx[strings.ToLower(name)] = a
	}

	return idx
})

// provAttrList are the attributes RunProv writes to LDAP itself; a command
// that touches any other attribute (and every creation verb) goes to zmprov,
// which runs the Java attribute callbacks and the full value checks and so
// stays correct by default.
//
// zmprov does three things around a modify that a plain LDAP write skips:
//
//   - the callback named by callback= in attrs.xml (LdapAttributeCallbackHelper
//     preModify validates, postModify may write other attributes or
//     localconfig);
//   - checkValue (type, range, enum, length) of the new values;
//   - binary I/O: the value of a binary attribute is a file path.
//
// An attribute is listed only after the callback was read and found absent or a
// no-op in bootstrap, which testdata/attrs-allowlist.tsv (a snapshot of the
// attrs.xml entries) and TestProvLDAPAttrs_MatchTheSnapshot pin; the value
// checks are done by provAttr.valid, and a value that fails them is sent to
// zmprov, which reports the error. Attributes with a callback that is not
// listed on purpose:
//
//   - zimbraMailMode (LocalBind): postModify writes zimbraMailLocalBind of the
//     server (every server for the global config);
//   - zimbraMailPort (MailPort): postModify writes zimbra_mail_service_port to
//     localconfig;
//   - the *BindPort, *ProxyBindPort, zimbraMailSSLPort, zimbraMailProxyPort and
//     zimbraMailSSLProxyPort ports (CheckPortConflict): preModify rejects a port
//     that another port of the server or global config already uses;
//   - zimbraMailHostPool (MailHostPool): preModify rejects an id that is not a
//     server with the webapp service.
//
// Add an attribute here only with its row in the snapshot (see
// scripts/check-attrs-drift.sh).
//
//nolint:goconst // the names are keys of this table; server.go spells some of them for its own batches
var provAttrList = map[string]provAttr{
	// Global config.

	// binary, no callback: zmprov reads the file and stores its content, as
	// provBinary does (the DH parameters, PEM text).
	"zimbraSSLDHParam": {kind: provBinary},
	// string, no callback.
	"zimbraLDAPSchemaVersion": {kind: provString},
	// multi-valued string, no callback: + adds only a missing value.
	"zimbraReverseProxyAvailableLookupTargets": {kind: provString},
	"zimbraReverseProxyUpstreamEwsServers":     {kind: provString},
	// string, no callback.
	"zimbraSkinLogoURL":                   {kind: provString, max: 256},
	"zimbraPublicServiceHostname":         {kind: provString, max: 256},
	"zimbraReverseProxyMailHostQuery":     {kind: provString},
	"zimbraReverseProxyPortQuery":         {kind: provString},
	"zimbraReverseProxyDomainNameQuery":   {kind: provString},
	"zimbraReverseProxyMailHostAttribute": {kind: provString},
	// string, no callback.
	"zimbraReverseProxyPop3PortAttribute":    {kind: provString},
	"zimbraReverseProxyPop3SSLPortAttribute": {kind: provString},
	"zimbraReverseProxyImapPortAttribute":    {kind: provString},
	"zimbraReverseProxyImapSSLPortAttribute": {kind: provString},
	"zimbraReverseProxyDomainNameAttribute":  {kind: provString},
	// boolean, no callback.
	"zimbraImapCleartextLoginEnabled": {kind: provBool},
	"zimbraPop3CleartextLoginEnabled": {kind: provBool},
	"zimbraReverseProxyHttpEnabled":   {kind: provBool},
	"zimbraReverseProxyMailEnabled":   {kind: provBool},
	// duration, no callback.
	"zimbraReverseProxyAuthWaitInterval": {kind: provDuration},
	// integer (minimum 0), no callback.
	"zimbraReverseProxyIPLoginLimit":       {kind: provInt},
	"zimbraReverseProxyIPLoginLimitTime":   {kind: provInt},
	"zimbraReverseProxyUserLoginLimit":     {kind: provInt},
	"zimbraReverseProxyUserLoginLimitTime": {kind: provInt},
	// string, no callback.
	"zimbraDefaultDomainName": {kind: provString, max: 256},
	"zimbraMtaMyOrigin":       {kind: provString, max: 256},
	// email, no callback. The addresses are those of accounts the same batch
	// created with ca (or that exist), which zmprov already validated.
	"carbonioNotificationFrom":       {kind: provString, max: 256},
	"carbonioNotificationRecipients": {kind: provString, max: 256},
	"zimbraSpamIsSpamAccount":        {kind: provString, max: 256},
	"zimbraSpamIsNotSpamAccount":     {kind: provString, max: 256},
	"zimbraAmavisQuarantineAccount":  {kind: provString, max: 256},

	// Server.

	// string, no callback: the service lists; - removes only a present value.
	"zimbraServiceEnabled":   {kind: provString, max: 256},
	"zimbraServiceInstalled": {kind: provString, max: 256},
	// enum, no callback.
	"zimbraIPMode": {kind: provEnum, enum: []string{"ipv4", "ipv6", "both"}},
	// boolean, no callback.
	"zimbraReverseProxyLookupTarget":            {kind: provBool},
	"zimbraMtaAuthTarget":                       {kind: provBool},
	"zimbraReverseProxyStrictServerNameEnabled": {kind: provBool},
	// astring, no callback.
	"zimbraMtaMyNetworks": {kind: provString, max: 10240},

	// Class of service.

	// boolean, no callback.
	"zimbraPrefUseKeyboardShortcuts": {kind: provBool},
	// string, no callback.
	"zimbraPrefTimeZoneId": {kind: provString, max: 256},
}
