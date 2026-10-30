// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"fmt"
	"slices"
	"strings"
)

// Legacy zmlocalconfig option ids: the short letter, or the long name for
// options without one (LocalConfigCLI registers --all with no short form).
const (
	optConfig  = "c"
	optPath    = "p"
	optEdit    = "e"
	optRandom  = "r"
	optDefault = "d"
	optChanged = "n"
	optInfo    = "i"
	optAll     = "all"
	optExpand  = "x"
	optShow    = "s"
	optForce   = "f"
	optFormat  = "m"
	optQuiet   = "q"
	optUnset   = "u"
	optReload  = "l"
)

// optionOrder is the order in which legacy LocalConfigCLI declares its
// options; it breaks ties when the command line order is unknown.
var optionOrder = []string{
	optConfig, optPath, optEdit, optRandom, optDefault, optChanged, optInfo, optAll,
	optExpand, optShow, optForce, optFormat, optQuiet, optUnset, optReload,
}

// optionsWithValue are the options that consume the following argument.
var optionsWithValue = []string{optConfig, optFormat, "k"}

// longOptionIDs maps every accepted long flag name to its option id: the legacy
// LocalConfigCLI names (--config, --path, --default, --changed, --show, --format,
// ...) and the names Kong derives for LocalconfigCmd, so both spellings are
// ordered on the command line.
var longOptionIDs = map[string]string{
	"--config":         optConfig,
	"--config-path":    optConfig,
	"--path":           optPath,
	"--show-path":      optPath,
	"--edit":           optEdit,
	"--random":         optRandom,
	"--default":        optDefault,
	"--show-defaults":  optDefault,
	"--changed":        optChanged,
	"--show-changed":   optChanged,
	"--info":           optInfo,
	"--all":            optAll,
	"--expand":         optExpand,
	"--show":           optShow,
	"--show-passwords": optShow,
	"--force":          optForce,
	"--format":         optFormat,
	"--mode":           optFormat,
	"--quiet":          optQuiet,
	"--unset":          optUnset,
	"--reload":         optReload,
	"--key":            "k",
}

// lcFailure is a fatal legacy error: the message is printed as "Error: <msg>" on
// stderr (unless -q) and the process exits with status 1, like
// LocalConfigCLI.error -> Logging.error + System.exit(1).
//
// detail, when set, is the legacy "e.toString()" line printed after the message
// when the failure carries an exception.
type lcFailure struct{ msg, detail string }

func (e *lcFailure) Error() string { return e.msg }

// failDetail is a failure that also reports the underlying cause on a second
// line, like Logging.error(msg, exception).
func failDetail(msg string, cause error) error {
	return &lcFailure{msg: msg, detail: cause.Error()}
}

func failf(format string, args ...any) error {
	return &lcFailure{msg: fmt.Sprintf(format, args...)}
}

// givenOptions lists, in declaration order, the legacy options present in c.
func (c *Options) givenOptions() []string {
	present := map[string]bool{
		optConfig:  c.ConfigPath != "",
		optPath:    c.ShowPath,
		optEdit:    c.Edit,
		optRandom:  c.Random,
		optDefault: c.ShowDefaults,
		optChanged: c.ShowChanged,
		optInfo:    c.Info,
		optAll:     c.All,
		optExpand:  c.Expand,
		optShow:    c.ShowPasswords,
		optForce:   c.Force,
		optFormat:  c.Mode != "",
		optQuiet:   c.Quiet,
		optUnset:   c.Unset,
		optReload:  c.Reload,
	}

	given := make([]string, 0, len(present))

	for _, id := range optionOrder {
		if present[id] {
			given = append(given, id)
		}
	}

	return given
}

// optionSequence returns the option ids in the order they appear on the
// "configd localconfig ..." command line. It only orders what Kong already
// accepted; without a "localconfig" word in args (tests) it returns nil.
func optionSequence(args []string) []string {
	start := slices.Index(args, "localconfig")
	if start < 0 {
		return nil
	}

	var seq []string

	rest := args[start+1:]

	for i := 0; i < len(rest); i++ {
		arg := rest[i]

		switch {
		case arg == "--":
			return seq
		case strings.HasPrefix(arg, "--"):
			name, _, hasValue := strings.Cut(arg, "=")

			id, ok := longOptionIDs[name]
			if !ok {
				continue
			}

			seq = append(seq, id)

			if slices.Contains(optionsWithValue, id) && !hasValue {
				i++
			}
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			seq, i = appendShortOptions(seq, arg, i)
		}
	}

	return seq
}

// appendShortOptions records the (possibly clustered) short flags of arg and
// advances i past the value argument of a trailing value-taking flag.
func appendShortOptions(seq []string, arg string, i int) (newSeq []string, newI int) {
	for j := 1; j < len(arg); j++ {
		id := arg[j : j+1]
		seq = append(seq, id)

		if slices.Contains(optionsWithValue, id) {
			if j == len(arg)-1 {
				i++
			}

			break
		}
	}

	return seq, i
}

// orderGivenOptions sorts given by first appearance in seq; options missing from
// seq keep their relative order after the ones found.
func orderGivenOptions(given, seq []string) []string {
	rank := func(id string) int {
		if idx := slices.Index(seq, id); idx >= 0 {
			return idx
		}

		return len(seq)
	}

	ordered := slices.Clone(given)
	slices.SortStableFunc(ordered, func(a, b string) int { return rank(a) - rank(b) })

	return ordered
}

// checkCompatible ports LocalConfigCLI.checkCompatibleOptions: every option
// present other than mainOption must be in the allowed set (a string of option
// letters, matched with String.contains semantics). The first offender in
// command-line order is reported.
func (o *localconfigOpts) checkCompatible(mainOption, allowed string) error {
	for _, id := range o.given {
		if id == mainOption || strings.Contains(allowed, id) {
			continue
		}

		if mainOption == "" {
			return failf("invalid option '%s'", id)
		}

		return failf("option '%s' can not be used with option '%s'", id, mainOption)
	}

	return nil
}
