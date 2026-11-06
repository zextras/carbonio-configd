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
  local prev="${COMP_WORDS[COMP_CWORD-1]}"
  local flags="-m -c -q -p -s -d -n -e -u -r -f -k -x -i -l --force --info --reload"
  local modes="plain shell export nokey xml"

  if [[ "$cur" == -* ]]; then
    COMPREPLY=($(compgen -W "$flags" -- "$cur"))
    return
  fi

  if [[ "$prev" == "-m" ]]; then
    COMPREPLY=($(compgen -W "$modes" -- "$cur"))
    return
  fi

  # Key names come from configd itself; its last output line is the
  # ":<directive>" marker, which is dropped. Without configd, offer the flags.
  local keys line
  while IFS= read -r line; do
    [[ "$line" == :* ]] || keys+="$line "
  done < <(configd __completeNoDesc localconfig "$cur" 2>/dev/null)
  if [[ -z "$keys" ]]; then
    COMPREPLY=($(compgen -W "$flags" -- "$cur"))
    return
  fi

  # -e takes key=value: complete "key=" and let the value follow directly.
  if [[ " ${COMP_WORDS[*]:1:COMP_CWORD-1} " == *" -e "* ]]; then
    COMPREPLY=($(compgen -W "$keys" -S '=' -- "$cur"))
    compopt -o nospace
    return
  fi

  COMPREPLY=($(compgen -W "$keys" -- "$cur"))
}
complete -F _zmlocalconfig_completions zmlocalconfig

# zmconfigdctl — wrapper for `configd service <verb> configd`
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
  local flags="-m -n -e --markers --no-comments --no-empty"
  COMPREPLY=($(compgen -W "$flags" -- "$cur"))
}
complete -F _zmproxyconf_completions zmproxyconf

# zmproxyconfgen — delegates to configd proxy gen
_zmproxyconfgen_completions() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
}
complete -F _zmproxyconfgen_completions zmproxyconfgen
