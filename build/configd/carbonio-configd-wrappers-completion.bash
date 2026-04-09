# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Bash completions for configd compatibility wrappers.

# zmcontrol — delegates to configd control
_zmcontrol_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local actions="start stop restart status startup shutdown"
  local flags="-v -V -h -H --help"
  COMPREPLY=($(compgen -W "$actions $flags" -- "$cur"))
}
complete -F _zmcontrol_completions zmcontrol

# zmlocalconfig — delegates to configd localconfig
_zmlocalconfig_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local flags="-m -c -q -p -s -d -n -e -u -r -f -k -x --force"
  local modes="plain shell export nokey xml"
  if [[ "$cur" == -* ]]; then
    COMPREPLY=($(compgen -W "$flags" -- "$cur"))
  elif [[ " ${COMP_WORDS[*]} " == *" -m "* ]]; then
    COMPREPLY=($(compgen -W "$modes" -- "$cur"))
  else
    COMPREPLY=($(compgen -W "$flags" -- "$cur"))
  fi
}
complete -F _zmlocalconfig_completions zmlocalconfig

# zmconfigdctl — delegates to systemctl for carbonio-configd.service
_zmconfigdctl_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=($(compgen -W "start stop restart reload status" -- "$cur"))
}
complete -F _zmconfigdctl_completions zmconfigdctl

# zmtlsctl — delegates to configd tls
_zmtlsctl_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local modes="both http https mixed redirect"
  local flags="--force --host help --help -help"
  COMPREPLY=($(compgen -W "$modes $flags" -- "$cur"))
}
complete -F _zmtlsctl_completions zmtlsctl

# zm*ctl service wrappers — all delegate to configd service <action> <name>
_zmctl_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=($(compgen -W "start stop restart reload status" -- "$cur"))
}
complete -F _zmctl_completions zmmtactl
complete -F _zmctl_completions zmproxyctl
complete -F _zmctl_completions zmamavisdctl
complete -F _zmctl_completions zmantivirusctl
complete -F _zmctl_completions zmantispamctl
complete -F _zmctl_completions zmcbpolicydctl
complete -F _zmctl_completions zmclamdctl
complete -F _zmctl_completions zmfreshclamctl
complete -F _zmctl_completions zmmemcachedctl
complete -F _zmctl_completions zmmilterctl
complete -F _zmctl_completions zmopendkimctl
complete -F _zmctl_completions zmsaslauthdctl
complete -F _zmctl_completions zmstorectl
complete -F _zmctl_completions zmmailboxdctl
complete -F _zmctl_completions zmstatctl

# zmproxyconf — delegates to configd proxy conf
_zmproxyconf_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local flags="-m -i -n -e --markers --indent --no-comments --no-empty"
  COMPREPLY=($(compgen -W "$flags" -- "$cur"))
}
complete -F _zmproxyconf_completions zmproxyconf

# zmproxyconfgen — delegates to configd proxy gen
_zmproxyconfgen_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
}
complete -F _zmproxyconfgen_completions zmproxyconfgen
