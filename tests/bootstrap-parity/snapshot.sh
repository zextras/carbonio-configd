#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Runs INSIDE the container (as root): captures the normalised state of a
# bootstrapped Carbonio node into <outdir>.
#
#   parity-snapshot /tmp/parity-snapshot
#
# Files produced (all deterministic for a given configuration):
#   ldap-config.ldif   slapcat of cn=config (olc*), normalised
#   ldap-main.ldif     slapcat of the main DB (cn=zimbra tree), normalised
#   localconfig.txt    sorted "key = value"; secret values -> <secret>/<empty>
#   crontab.txt        crontab -u zextras -l
#   conf-files.txt     path, type, mode, owner:group, sha256 (or <volatile>)
#   systemd-units.txt  enabled unit files + active units
#   install-history.txt .install_history without the epoch column
#   listeners.txt      listening TCP ports
set -uo pipefail

OUT="${1:?usage: parity-snapshot OUTDIR}"
EXCLUDE_FILE="${PARITY_EXCLUDE_FILE:-/usr/local/share/parity-snapshot-exclude.txt}"
NORMALISE="${PARITY_NORMALISE:-/usr/local/share/parity-normalise-ldif.pl}"
ZX=/opt/zextras
rm -rf "${OUT}"
mkdir -p "${OUT}"
export LC_ALL=C

slapcat_as_zextras() {
  runuser -u zextras -- env LD_PRELOAD=/opt/zextras/common/lib/libjemalloc.so \
    /opt/zextras/common/sbin/slapcat -F "${ZX}/data/ldap/config" "$@"
}

# --- LDAP --------------------------------------------------------------------
if [[ -x ${ZX}/common/sbin/slapcat && -d ${ZX}/data/ldap/config ]]; then
  slapcat_as_zextras -n 0 2>/dev/null | perl "${NORMALISE}" >"${OUT}/ldap-config.ldif"
  slapcat_as_zextras -b "" 2>/dev/null | perl "${NORMALISE}" >"${OUT}/ldap-main.ldif"
fi

# --- localconfig.xml ----------------------------------------------------------
# Secrets: key name containing pass/secret/pw/token/_key, or keystore password.
if [[ -f ${ZX}/conf/localconfig.xml ]]; then
  perl -0777 -ne '
    my %h;
    while (/<key name="([^"]+)">\s*<value>(.*?)<\/value>\s*<\/key>/gs) {
      my ($k, $v) = ($1, $2);
      $v = (length $v ? "<secret>" : "<empty>")
        if $k =~ /(pass|secret|_pw$|token|_key$|keystore_password|credential)/i;
      $h{$k} = $v;
    }
    print "$_ = $h{$_}\n" for sort keys %h;
  ' "${ZX}/conf/localconfig.xml" >"${OUT}/localconfig.txt"
fi

# --- crontab ------------------------------------------------------------------
crontab -u zextras -l >"${OUT}/crontab.txt" 2>&1 || true

# --- file tree ----------------------------------------------------------------
# Globs of files whose content is volatile; they stay listed with owner/mode.
mapfile -t EXCL < <(grep -vE '^\s*(#|$)' "${EXCLUDE_FILE}" 2>/dev/null)
is_volatile() {
  local p="$1" g
  for g in "${EXCL[@]}"; do
    # shellcheck disable=SC2053 # intentional glob match
    [[ ${p} == ${g} ]] && return 0
  done
  return 1
}
{
  for root in "${ZX}/conf" /etc/rsyslog.d /etc/logrotate.d /etc/cron.d /etc/sudoers.d; do
    [[ -e ${root} ]] || continue
    find "${root}" -mindepth 0 -printf '%p\t%y\t%m\t%u:%g\t%l\n' | sort
  done
} | while IFS=$'\t' read -r path type mode owner link; do
  case "${type}" in
    f)
      if is_volatile "${path}"; then
        sum='<volatile>'
      else
        sum="$(sha256sum <"${path}" | cut -d' ' -f1)"
      fi
      ;;
    l) sum="-> ${link}" ;;
    *) sum='-' ;;
  esac
  printf '%s\t%s\t%s\t%s\t%s\n' "${path}" "${type}" "${mode}" "${owner}" "${sum}"
done >"${OUT}/conf-files.txt"

# --- systemd ------------------------------------------------------------------
{
  echo '## enabled unit files'
  systemctl list-unit-files --no-legend --no-pager --state=enabled,enabled-runtime 2>/dev/null |
    awk '{print $1, $2}' | sort
  echo '## active units'
  systemctl list-units --no-legend --no-pager --plain --state=active 2>/dev/null |
    awk '{print $1}' | grep -E '\.(service|timer|socket|target)$' |
    grep -vE '^(user@[0-9]+\.service|run-[a-z0-9]+\.service)$' | sort
  echo '## failed units'
  systemctl list-units --no-legend --no-pager --plain --state=failed 2>/dev/null | awk '{print $1}' | sort
} >"${OUT}/systemd-units.txt"

# --- install history ----------------------------------------------------------
if [[ -f ${ZX}/.install_history ]]; then
  sed -E 's/^[0-9]+: //' "${ZX}/.install_history" >"${OUT}/install-history.txt"
else
  : >"${OUT}/install-history.txt"
fi

# --- listening sockets (service start parity) ---------------------------------
ss -H -ltn 2>/dev/null | awk '{n=split($4,a,":"); print a[n]}' | sort -n -u >"${OUT}/listeners.txt"

echo "snapshot complete: ${OUT}"
