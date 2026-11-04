#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# One-time setup (as zextras): a stand-alone slapd with the product schema and
# the product's seed entries, and the localconfig keys that point the legacy
# tools at it. This mirrors what the product bootstrap does for a directory
# server, minus TLS, replication and the real passwords.
set -euo pipefail
. /golden/bin/lib.sh

cp -r "$ZM"/common/etc/openldap/zimbra/config/* "$LDAP_CFG"/
sed -i "s|^olcRootPW::.*|olcRootPW: $LDAP_PW|" "$LDAP_CFG/cn=config/olcDatabase={0}config.ldif"
"$ZM/libexec/zmldapschema" >/dev/null
# slapd refuses to start on the packaged TLS settings without certificates.
sed -i '/^olcTLS/d' "$LDAP_CFG/cn=config.ldif"

# The pristine template trees every case starts from.
rm -rf "$STATE/pristine"
mkdir -p "$STATE/pristine"
cp -a "$ZM/conf" "$STATE/pristine/conf"
cp -a "$ZM/common/conf" "$STATE/pristine/common-conf"
cp -a "$ZM/data/spamassassin/localrules" "$STATE/pristine/localrules"
