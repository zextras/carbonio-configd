#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Empty the directory and reload the product seed entries.
set -euo pipefail
. /golden/bin/lib.sh

pkill -u zextras -x slapd 2>/dev/null || true
for _ in $(seq 50); do pgrep -u zextras -x slapd >/dev/null || break; sleep 0.1; done
rm -rf "$ZM"/data/ldap/mdb/db/*

nohup "$ZM/common/libexec/slapd" -d 0 -h "$LDAP_URL" -F "$LDAP_CFG" >"$STATE/slapd.log" 2>&1 &
for _ in $(seq 100); do
	ldapsearch -x -H "$LDAP_URL" -s base -b '' namingContexts >/dev/null 2>&1 && break
	sleep 0.1
done

for f in carbonio zimbra_globalconfig zimbra_defaultcos zimbra_defaultexternalcos mimehandlers; do
	ldapadd -x -H "$LDAP_URL" -D cn=config -w "$LDAP_PW" -f "$SEEDS/$f.ldif" >/dev/null
done
ldapmodify -x -H "$LDAP_URL" -D cn=config -w "$LDAP_PW" >/dev/null <<LDIF
dn: uid=zimbra,cn=admins,cn=zimbra
changetype: modify
replace: userPassword
userPassword: $LDAP_PW
LDIF
