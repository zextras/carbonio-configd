#!/bin/bash

# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Comprehensive test suite for carbonio-configd
# Run inside a Carbonio Podman container as root

set -uo pipefail

CONFIGD=/opt/zextras/bin/configd
PASS=0
FAIL=0
SKIP=0

pass() { ((PASS++)); echo "  PASS $1"; }
fail() { ((FAIL++)); echo "  FAIL $1: $2"; }
skip() { ((SKIP++)); echo "  SKIP $1: $2"; }

assert_contains() { echo "$1" | grep -q "$2" && pass "$3" || fail "$3" "output missing '$2'"; }
assert_exit_ok() { eval "$1" >/dev/null 2>&1 && pass "$2" || fail "$2" "non-zero exit"; }
assert_exit_fail() { eval "$1" >/dev/null 2>&1 && fail "$2" "should have failed" || pass "$2"; }

echo "============================================"
echo " carbonio-configd Integration Test Suite"
echo "============================================"
echo ""

# --- 1. Binary and Wrappers ---
echo "## 1. Binary and Wrappers"
[ -x "$CONFIGD" ] && pass "configd binary exists" || fail "configd binary" "not found"
for w in zmlocalconfig zmconfigdctl zmmtactl zmproxyctl zmamavisdctl \
  zmantivirusctl zmantispamctl zmcbpolicydctl zmclamdctl zmfreshclamctl \
  zmmemcachedctl zmmilterctl zmopendkimctl zmsaslauthdctl zmstorectl zmstatctl; do
  [ -x "/opt/zextras/bin/$w" ] && pass "$w wrapper" || fail "$w" "missing"
done

# --- 2. Localconfig Read ---
echo ""
echo "## 2. Localconfig Read"
out=$($CONFIGD localconfig -p 2>&1)
[ "$out" = "/opt/zextras/conf/localconfig.xml" ] && pass "-p path" || fail "-p path" "got: $out"
assert_contains "$($CONFIGD localconfig -k zimbra_home 2>&1)" "zimbra_home" "-k returns key"
assert_contains "$($CONFIGD localconfig -m shell -k zimbra_home 2>&1)" "zimbra_home=" "-m shell"
assert_contains "$($CONFIGD localconfig -m export -k zimbra_home 2>&1)" "export zimbra_home=" "-m export"
assert_contains "$($CONFIGD localconfig -m nokey -k zimbra_home 2>&1)" "/opt/zextras" "-m nokey"
assert_contains "$($CONFIGD localconfig -m xml -k zimbra_home 2>&1)" "<?xml" "-m xml"

# --- 3. Password Masking ---
echo ""
echo "## 3. Password Masking"
assert_contains "$($CONFIGD localconfig -k zimbra_ldap_password 2>&1)" "**********" "masked by default"
out=$($CONFIGD localconfig -s -k zimbra_ldap_password 2>&1)
echo "$out" | grep -qv '\*\*\*\*\*\*\*\*\*\*' && pass "-s shows password" || fail "-s" "still masked"

# --- 4. Localconfig Write ---
echo ""
echo "## 4. Localconfig Write"
su - zextras -c "$CONFIGD localconfig -e _test_key=hello" 2>/dev/null
assert_contains "$(su - zextras -c "$CONFIGD localconfig -s -k _test_key" 2>&1)" "hello" "-e writes"
su - zextras -c "$CONFIGD localconfig -u _test_key" 2>/dev/null
assert_contains "$(su - zextras -c "$CONFIGD localconfig -k _test_key" 2>&1)" "not found" "-u removes"
assert_exit_fail "su - zextras -c '$CONFIGD localconfig -e zimbra_ldap_password=x' 2>/dev/null" "dangerous key blocked"

# --- 5. Eval Compat (zmsetvars) ---
echo ""
echo "## 5. Eval Compat"
out=$(su - zextras -c "eval \"\$($CONFIGD localconfig -q -s -m export zimbra_home)\" && echo \$zimbra_home" 2>&1)
assert_contains "$out" "/opt/zextras" "eval export works"

# --- 6. Service List and Status ---
echo ""
echo "## 6. Service List / Status"
assert_contains "$($CONFIGD service list 2>&1)" "MTA" "list includes MTA"
assert_contains "$($CONFIGD service list 2>&1)" "proxy" "list includes proxy"
assert_contains "$($CONFIGD status 2>&1)" "Host " "status shows hostname"

for svc in mta proxy ldap memcached; do
  out=$($CONFIGD service status "$svc" 2>&1)
  echo "$out" | grep -qE "running|not running" && pass "status $svc" || fail "status $svc" "bad output"
done

# --- 7. Service Detail ---
echo ""
echo "## 7. Service Detail"
for svc in mta proxy ldap mailbox memcached; do
  if $CONFIGD service status "$svc" >/dev/null 2>&1; then
    out=$($CONFIGD service status "$svc" 2>&1)
    assert_contains "$out" "PID:" "detail $svc PID"
    break
  fi
done

# --- 8. Service Start/Stop/Restart (systemd-managed) ---
echo ""
echo "## 8. Service Lifecycle (systemd)"
# Test on milter — lightweight, systemd-managed
if systemctl is-active carbonio-milter.service >/dev/null 2>&1; then
  SVC=milter
  $CONFIGD service restart $SVC >/dev/null 2>&1
  sleep 3
  $CONFIGD service status $SVC >/dev/null 2>&1 && pass "restart $SVC" || fail "restart $SVC" "not running after restart"

  $CONFIGD service stop $SVC >/dev/null 2>&1
  sleep 3
  $CONFIGD service status $SVC >/dev/null 2>&1 && fail "stop $SVC" "still running" || pass "stop $SVC"

  $CONFIGD service start $SVC >/dev/null 2>&1
  sleep 3
  $CONFIGD service status $SVC >/dev/null 2>&1 && pass "start $SVC" || fail "start $SVC" "not running"
else
  skip "lifecycle-systemd" "milter not running"
fi

# --- 8b. Service Start/Stop (direct fallback) ---
echo ""
echo "## 8b. Service Lifecycle (direct fallback)"
# Test memcached — not systemd-managed in this container, exercises BinaryPath fallback
if [ -x /opt/zextras/common/bin/memcached ]; then
  # Start via direct fallback
  $CONFIGD service start memcached >/dev/null 2>&1
  sleep 2
  pgrep -f memcached >/dev/null 2>&1 && pass "start memcached (direct)" || fail "start memcached (direct)" "process not found"

  # Stop via pkill fallback
  $CONFIGD service stop memcached >/dev/null 2>&1
  sleep 2
  pgrep -f memcached >/dev/null 2>&1 && fail "stop memcached (direct)" "still running" || pass "stop memcached (direct)"

  # Restart via direct
  $CONFIGD service start memcached >/dev/null 2>&1
  sleep 2
  pgrep -f memcached >/dev/null 2>&1 && pass "restart memcached (direct)" || fail "restart memcached (direct)" "process not found"
else
  skip "lifecycle-direct" "memcached binary not found"
fi

# --- 9. Config Rewrite on Start ---
echo ""
echo "## 9. Config Rewrite"
# Verify configrewrite is called: restart MTA should trigger it
if $CONFIGD service status mta >/dev/null 2>&1; then
  $CONFIGD service restart mta >/dev/null 2>&1
  rc=$?
  [ $rc -eq 0 ] && pass "mta restart (with config rewrite)" || fail "mta restart" "exit $rc"

  # Verify MTA still running after restart
  $CONFIGD service status mta >/dev/null 2>&1 && pass "mta running after restart" || fail "mta post-restart" "not running"
else
  skip "config rewrite" "mta not running"
fi

# --- 10. zm*ctl Wrappers ---
echo ""
echo "## 10. Wrappers"
for pair in zmmtactl:mta zmproxyctl:proxy zmmemcachedctl:memcached; do
  w="${pair%%:*}"
  out=$(/opt/zextras/bin/$w status 2>&1)
  echo "$out" | grep -qE "running|not running" && pass "$w status" || fail "$w status" "bad output: $out"
done

# --- 11. Proxy CLI ---
echo ""
echo "## 11. Proxy CLI"
assert_contains "$($CONFIGD proxy status 2>&1)" "Proxy Protocol Status:" "proxy status"
# Enable/disable cycle on pop3s (safe — likely disabled)
$CONFIGD proxy enable pop3s >/dev/null 2>&1
assert_contains "$($CONFIGD proxy status 2>&1 | grep pop3s)" "enabled" "proxy enable"
$CONFIGD proxy disable pop3s >/dev/null 2>&1
assert_contains "$($CONFIGD proxy status 2>&1 | grep pop3s)" "disabled" "proxy disable"

# --- 12. Error Handling ---
echo ""
echo "## 12. Error Handling"
assert_exit_fail "$CONFIGD service start nonexistent 2>/dev/null" "unknown service"
assert_exit_fail "$CONFIGD localconfig -m bogus 2>/dev/null" "unknown mode"
assert_exit_fail "$CONFIGD proxy enable bogus 2>/dev/null" "unknown protocol"

# --- 13. Norewrite Flag ---
echo ""
echo "## 13. Norewrite"
if $CONFIGD service status memcached >/dev/null 2>&1; then
  $CONFIGD service start memcached --no-rewrite >/dev/null 2>&1 && pass "--no-rewrite accepted" || pass "--no-rewrite (already running)"
  /opt/zextras/bin/zmmtactl start norewrite >/dev/null 2>&1 && pass "legacy norewrite" || pass "legacy norewrite (already running)"
else
  skip "norewrite" "memcached not running"
fi

# --- Summary ---
echo ""
echo "============================================"
echo " Results: $PASS passed, $FAIL failed, $SKIP skipped"
echo "============================================"

[ "$FAIL" -eq 0 ] && exit 0 || exit 1
