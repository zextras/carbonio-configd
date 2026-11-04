#!/bin/bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Root part of the container setup: replace the commands zmconfigd shells out
# to for side effects with recorders, and prepare the state directory.
set -euo pipefail
. /golden/bin/lib.sh

install -d -o zextras -g zextras "$STATE" "$ZM/log"

for c in postconf ldap zmamavisdctl zmantispamctl zmantivirusctl zmcbpolicydctl zmclamdctl \
         zmfreshclamctl zmmailboxdctl zmmemcachedctl zmmilterctl zmmtactl zmopendkimctl \
         zmproxyctl zmresolverctl zmsaslauthdctl zmstatctl zmstorectl; do
	ln -sf /golden/bin/recorder "$ZM/bin/$c"
done
chmod 0755 /golden/bin/*
