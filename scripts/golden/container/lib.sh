# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Shared settings of the in-container legacy runner (sourced, not executed).

export ZM=/opt/zextras
export PATH=$ZM/bin:$ZM/common/bin:$ZM/common/sbin:$PATH
# The legacy Jython modules; the carbonio-configd unit sets the same variable.
export JYTHONPATH=$ZM/common/lib/jylibs
# Files are created 0640 like under the real zextras account.
umask 0027

STATE=/var/tmp/golden
LDAP_URL=ldap://127.0.0.1:389
# Throwaway LDAP password of the fixture directory; it is also the value of the
# fixture localconfig keys, so nothing here is a real credential.
LDAP_PW=GOLDEN-ldap-root-password
LDAP_CFG=$ZM/data/ldap/config
SEEDS=$ZM/common/etc/openldap/zimbra
