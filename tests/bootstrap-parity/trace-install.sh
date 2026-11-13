#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only
#
# Runs INSIDE the container (run-legacy.sh --trace copies and runs it): records
# every start of the Java tools the native setup replaces, without changing what
# they do. zmprov and zmjava are moved to <name>.real and replaced by a wrapper
# that appends one line to /var/log/parity-trace.log (never the arguments: a
# zmprov call carries passwords; only the tool, the Java class for zmjava and
# the chain of ancestor processes, with the script name of a perl or bash one)
# and execs the real one. The log is writable by everyone: zextras runs tools too. A sampler of the
# process table adds the processes of ProvUtil, RandomPassword and the Perl
# zmserverips, in /var/log/parity-ps.log.
set -euo pipefail

for tool in zmprov zmjava; do
  f=/opt/zextras/bin/${tool}
  [[ -e ${f}.real ]] || mv "${f}" "${f}.real"
  cat >"${f}" <<WRAP
#!/bin/bash
cls=""
[[ "${tool}" != zmjava ]] || cls="\${1:-}"
chain=""
p=\$PPID
while [[ \${p} -gt 1 ]]; do
  c=\$(ps -o comm= -p \${p} 2>/dev/null) || break
  case \${c} in
    perl|bash) c="\${c}:\$(basename "\$(ps -o args= -p \${p} | awk '{print \$2}')")" ;;
  esac
  chain="\${chain} \${c}"
  p=\$(ps -o ppid= -p \${p} 2>/dev/null | tr -d ' ')
  [[ -n \${p} ]] || break
done
echo "\$(date +%s.%N) ${tool} class=\${cls} chain:\${chain}" >>/var/log/parity-trace.log 2>/dev/null
exec ${f}.real "\$@"
WRAP
  chmod 0755 "${f}"
done

: >/var/log/parity-trace.log
chmod 0666 /var/log/parity-trace.log
nohup bash -c 'while true; do ps -eo pid,args | grep -E "ProvUtil|RandomPassword|perl .*serverips|zmserverips" | grep -vE "grep|parity-ps" >>/var/log/parity-ps.log; sleep 0.1; done' >/dev/null 2>&1 &
echo $! >/var/run/parity-sampler.pid
