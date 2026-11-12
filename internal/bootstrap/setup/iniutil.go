// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// iniOp is what zminiutil does with a key.
type iniOp int

const (
	iniNone iniOp = iota
	iniGet
	iniIsSet
	iniList
	iniSet
	iniSetMin
	iniSetMax
	iniUnset
)

// iniEdit is one zminiutil command: the operation, the key and the backup
// file suffix of the operations that rewrite the file.
type iniEdit struct {
	Op           iniOp
	Section, Key string
	Value        string
	HasValue     bool
	Backup       string
	HasBackup    bool
	// HasSection is set when --section was given (a list needs none).
	HasSection bool
}

// iniLine is one line of an ini file, as libexec/iniutil.pl parseFile keeps it.
type iniLine struct {
	content string
	// header is set for a "[section]" line, with its name (the characters
	// between the brackets, blanks included).
	header     bool
	newSection string
	// hasSection and section are the section a key line is in; hasKey is
	// false for a line that did not parse (more than one word after the =).
	hasSection bool
	section    string
	hasKey     bool
	key, value string
}

// The regular expressions of parseFile, matched against the line with its line
// end: Perl's $ also matches before it, which "\n?" does here, and the \s* that
// comes last takes the line end first as it does in Perl.
var (
	iniSectionRE = regexp.MustCompile(`^\s*\[(\s*\w*\s*)\]\s*(?:#.*)?\n?$`)
	iniEmptyRE   = regexp.MustCompile(`^\s*(?:#.*)?\n?$`)
	iniKeyRE     = regexp.MustCompile(`^(\s*)([^=\s]*)(\s*)(=?)(\s*)(\S*)(\s*(?:#.*)?)\n?$`)
)

// iniParse reads the lines of an ini file (iniutil.pl parseFile). dbg, when not
// nil, receives the lines of --debug.
func iniParse(content string, dbg io.Writer) []iniLine {
	var (
		lines  []iniLine
		cur    string
		hasCur bool
	)

	for raw := range strings.SplitAfterSeq(content, "\n") {
		if raw == "" {
			continue
		}

		line := iniLine{content: raw}

		if m := iniSectionRE.FindStringSubmatch(raw); m != nil {
			iniDebugf(dbg, "parse: new section=%s\n\n", m[1])

			cur, hasCur = m[1], true
			line.header, line.newSection = true, m[1]
			lines = append(lines, line)

			continue
		}

		if iniEmptyRE.MatchString(raw) {
			lines = append(lines, line)

			continue
		}

		line.hasSection, line.section = hasCur, cur
		m := iniKeyRE.FindStringSubmatch(raw)

		if m != nil {
			line.hasKey, line.key, line.value = true, m[2], m[6]
		}

		iniDebugLine(dbg, raw, &line, m)

		lines = append(lines, line)
	}

	return lines
}

func iniDebugf(dbg io.Writer, format string, args ...any) {
	if dbg != nil {
		_, _ = fmt.Fprintf(dbg, format, args...)
	}
}

// iniDebugLine prints what --debug printed for a line: its content and the
// groups of the match, "<undef>" for those a line that did not parse lacks.
func iniDebugLine(dbg io.Writer, raw string, line *iniLine, m []string) {
	if dbg == nil {
		return
	}

	dp := func(k string, v string, defined bool) {
		if defined {
			iniDebugf(dbg, "parser: %s='%s'\n", k, v)
		} else {
			iniDebugf(dbg, "parser: %s=<undef>\n", k)
		}
	}

	group := func(i int) (string, bool) {
		if m == nil {
			return "", false
		}

		return m[i], true
	}

	dp("==== content", raw, true)
	dp("section", line.section, line.hasSection)

	for _, g := range []struct {
		name string
		idx  int
	}{{"keyPre", 1}, {"key", 2}, {"keyPost", 3}, {"equals", 4}, {"valuePre", 5}, {"value", 6}, {"valuePost", 7}} {
		v, ok := group(g.idx)
		dp(g.name, v, ok)
	}
}

// iniKeys is the value of every "section->key" of the file; the last line of
// a key wins.
func iniKeys(lines []iniLine) map[string]string {
	keys := map[string]string{}

	for i := range lines {
		if l := &lines[i]; l.hasSection && l.hasKey {
			keys[l.section+"->"+l.key] = l.value
		}
	}

	return keys
}

// iniRender is the file after a set, setmin, setmax or unset (the main loop of
// iniutil.pl).
func iniRender(lines []iniLine, ed *iniEdit) string {
	var out strings.Builder

	_, defined := iniKeys(lines)[ed.Section+"->"+ed.Key]
	setting := ed.Op == iniSet || ed.Op == iniSetMin || ed.Op == iniSetMax

	assignment := ed.Key
	if ed.HasValue {
		assignment += " = " + ed.Value
	}

	for i := range lines {
		l := &lines[i]

		// A key the file lacks is added at the start of its section.
		if !defined && setting && l.header && l.newSection == ed.Section {
			out.WriteString(l.content + "\n" + assignment + "\n")

			continue
		}

		switch iniKeyLine(l, ed) {
		case iniReplace:
			out.WriteString(assignment + "\n")
		case iniKeep:
			out.WriteString(l.content)
		case iniDrop:
			continue
		}
	}

	return out.String()
}

// iniFate is what an edit does to a line.
type iniFate int

const (
	iniKeep iniFate = iota
	iniDrop
	iniReplace
)

// iniKeyLine tells what the edit does to the line: only a line of the key in
// the section is dropped (unset) or replaced.
func iniKeyLine(l *iniLine, ed *iniEdit) iniFate {
	if !l.hasKey || l.key != ed.Key || !l.hasSection || l.section != ed.Section {
		return iniKeep
	}

	switch {
	case ed.Op == iniUnset:
		return iniDrop
	case iniReplaces(ed, l.value):
		return iniReplace
	}

	return iniKeep
}

// iniReplaces tells whether the line holding current is replaced by the new
// value: always for set, for setmin when the new value is bigger, for setmax
// when it is smaller (a value that is not a number counts as 0).
func iniReplaces(ed *iniEdit, current string) bool {
	if ed.Op == iniSet {
		return true
	}

	if !iniIntRE.MatchString(current) {
		current = "0"
	}

	cur, _ := strconv.ParseFloat(current, 64)
	val, _ := strconv.ParseFloat(ed.Value, 64)

	switch ed.Op {
	case iniSetMin:
		return val > cur
	case iniSetMax:
		return val < cur
	default:
		return false
	}
}

var iniIntRE = regexp.MustCompile(`^[+-]?\d+$`)

// iniDie is a failure of the script that died: the message goes to stderr and
// the status is the errno, as Perl's die leaves it, else 255.
type iniDie struct{ err error }

func (d *iniDie) Error() string { return d.err.Error() }
func (d *iniDie) Unwrap() error { return d.err }

func (d *iniDie) status() int {
	if errno, ok := errors.AsType[syscall.Errno](d.err); ok && errno != 0 {
		return int(errno)
	}

	return 255
}

// iniApply runs ed on file. get, isset and list print to out and return their
// exit status; the other operations rewrite the file after saving it as
// file+backup (".bak" when none is given). A write keeps the mode and the
// owner of the file.
func iniApply(fsys sys.FS, file string, ed *iniEdit, out, dbg io.Writer) (status int, err error) {
	data, err := fsys.ReadFile(file)
	if err != nil {
		return 0, &iniDie{fmt.Errorf("can't open file %s: %w", file, err)}
	}

	lines := iniParse(string(data), dbg)
	keys := iniKeys(lines)

	switch ed.Op {
	case iniList:
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			_, _ = fmt.Fprintf(out, "%s=%s\n", k, keys[k])
		}

		return 0, nil
	case iniGet:
		v, ok := keys[ed.Section+"->"+ed.Key]
		if !ok {
			return 1, nil
		}

		_, _ = fmt.Fprintln(out, v)

		return 0, nil
	case iniIsSet:
		if _, ok := keys[ed.Section+"->"+ed.Key]; !ok {
			return 1, nil
		}

		return 0, nil
	}

	backup := file + ".bak"
	if ed.HasBackup {
		backup = file + ed.Backup
	}

	info, err := fsys.Stat(file)
	if err != nil {
		return 0, &iniDie{fmt.Errorf("can't open file %s: %w", file, err)}
	}

	if err := iniWrite(fsys, backup, data, info); err != nil {
		return 0, &iniDie{fmt.Errorf("can't open backup file: %s: %w", backup, err)}
	}

	if err := iniWrite(fsys, file, []byte(iniRender(lines, ed)), info); err != nil {
		return 0, &iniDie{fmt.Errorf("can't rename temp file to %s: %w", file, err)}
	}

	return 0, nil
}

// iniWrite writes name atomically with the mode and the owner of info.
func iniWrite(fsys sys.FS, name string, data []byte, info fs.FileInfo) error {
	if err := fsys.WriteFileAtomic(name, data, info.Mode().Perm()); err != nil {
		return err
	}

	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fsys.Chown(name, int(st.Uid), int(st.Gid))
	}

	return nil
}

const iniUsage = `Usage: 
  zminiutil [--help] \
    --backup=backup-suffix \
    --(list|isset|get|set|setmin|setmax|unset) \
    --section=s --key=k [--value=v] inifile

Eg:
  zminiutil --isset  --section=sec --key=lock srv.cnf
  zminiutil --get    --section=sec --key=fds srv.cnf
  zminiutil --backup=.bvx --set    --section=sec --key=lock srv.cnf
  zminiutil --backup=.baz --set    --section=sec --key=fds --value=12 srv.cnf
  zminiutil --backup=.bar --setmin --section=sec --key=foo --value=12 srv.cnf
  zminiutil --backup=.foo --setmax --section=sec --key=foo --value=12 srv.cnf
  zminiutil --backup=.foz --unset  --section=sec --key=bsz srv.cnf
`

// iniOptions are the options of zminiutil: the operations, then the rest.
var iniOptions = []optSpec{
	{names: []string{"debug"}}, {names: []string{optHelp}},
	{names: []string{"get"}}, {names: []string{"isset"}}, {names: []string{"list"}}, {names: []string{"set"}},
	{names: []string{"setmin"}}, {names: []string{"setmax"}}, {names: []string{"unset"}},
	{names: []string{"backup"}, kind: optString}, {names: []string{"section"}, kind: optString},
	{names: []string{"key"}, kind: optString}, {names: []string{"value"}, kind: optString},
}

// iniOperations are the operations by option name, in the order the script
// counts them.
var iniOperations = []struct {
	name string
	op   iniOp
}{
	{"get", iniGet}, {"isset", iniIsSet}, {"list", iniList}, {"set", iniSet},
	{"setmin", iniSetMin}, {"setmax", iniSetMax}, {"unset", iniUnset},
}

const iniOpNames = "[list|isset|get|set|setmin|setmax|unset]"

// iniCheck is the validation of libexec/iniutil.pl: the command it asks for
// and the file, or the message the script printed with "Error: " before exiting
// with status 1.
func iniCheck(o *optResult) (ed *iniEdit, msg string) {
	ed = &iniEdit{}

	ops := 0

	for _, c := range iniOperations {
		if o.has(c.name) {
			ed.Op = c.op
			ops++
		}
	}

	ed.Section, ed.HasSection = o.strs["section"], o.has("section")
	ed.Key = o.strs["key"]
	ed.Value, ed.HasValue = o.strs["value"], o.has("value")
	ed.Backup, ed.HasBackup = o.strs["backup"], o.has("backup")

	if msg = iniCheckAction(o, ed, ops); msg != "" {
		return nil, msg
	}

	if msg = iniCheckValue(o, ed); msg != "" {
		return nil, msg
	}

	return ed, ""
}

// iniCheckAction checks the action asked for and its section and key.
func iniCheckAction(o *optResult, ed *iniEdit, ops int) string {
	switch {
	case ops < 1:
		return "one of " + iniOpNames + " must be specified"
	case ops > 1:
		return "only one of " + iniOpNames + " must be specified"
	case !o.has("key") && ed.Op != iniList:
		return "--key not specified"
	case !ed.HasSection && ed.Op != iniList:
		return "--section not specified"
	}

	return ""
}

// iniCheckValue checks the value, the backup and the file argument.
func iniCheckValue(o *optResult, ed *iniEdit) string {
	setting := ed.Op == iniSet || ed.Op == iniSetMin || ed.Op == iniSetMax

	switch {
	case ed.HasValue && !setting:
		return "--value can only be specified with [set|setmin|setmax]"
	case !ed.HasValue && (ed.Op == iniSetMin || ed.Op == iniSetMax):
		return "--value must be specified with [setmin|setmax]"
	// The script's test lists set twice and not unset: unset takes the
	// default ".bak" backup.
	case !ed.HasBackup && setting:
		return "--backup must be specified with [set|setmin|setmax|unset]"
	case len(o.rest) != 1:
		return "one inifile must be specified"
	case (ed.Op == iniSetMin || ed.Op == iniSetMax) && !iniIntRE.MatchString(ed.Value):
		return "--value must be a number"
	}

	return ""
}

// RunIniUtil is zminiutil (libexec/iniutil.pl): it reads or edits one ini file
// with the options of the script and returns its exit status. Like the script
// it needs no particular user; its messages ("Error: ..." and the usage text)
// are on stdout, a failure to read or write the file on stderr with the errno
// as status.
func RunIniUtil(fsys sys.FS, args []string, stdout, stderr io.Writer) int {
	o := parseGetoptLong(args, iniOptions, stderr)

	if !o.allGood {
		_, _ = fmt.Fprintln(stdout, "Error: Unknown option!")

		return 1
	}

	if o.has(optHelp) {
		_, _ = io.WriteString(stdout, iniUsage)

		return 0
	}

	ed, msg := iniCheck(o)
	if msg != "" {
		_, _ = fmt.Fprintf(stdout, "Error: %s\n", msg)

		return 1
	}

	var dbg io.Writer
	if o.has("debug") {
		dbg = stdout
	}

	status, err := iniApply(fsys, o.rest[0], ed, stdout, dbg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		if d, ok := errors.AsType[*iniDie](err); ok {
			return d.status()
		}

		return 255
	}

	return status
}
