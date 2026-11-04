// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package golden loads the legacy-parity corpus under test/golden and compares
// what the Go configd produced for a case with what the legacy Jython zmconfigd
// and ProxyConfGen wrote. See README.md in this directory.
package golden

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Root returns the absolute path of test/golden.
func Root() string {
	_, file, _, _ := runtime.Caller(0)

	return filepath.Dir(file)
}

// Cycle is one configd cycle of a case.
type Cycle struct {
	N int
	// LDIF are the directory deltas applied before the cycle, in order.
	LDIF []string
	// LocalConfig is the localconfig.xml replacing the previous one, or "".
	LocalConfig string
	// Sections are the sections of a forced run (mode "forced").
	Sections []string
	Expected Expected
}

// Expected is what the legacy run produced in one cycle.
type Expected struct {
	// Files are the files the legacy run wrote, by path relative to the
	// install root (conf/amavisd.conf, common/conf/master.cf, ...).
	Files map[string][]byte
	// Modes are the permission bits (octal, as chmod prints them) of Files.
	Modes map[string]string
	// Exec is every command the legacy run executed: [name, args...].
	Exec [][]string
}

// Case is one scenario of the corpus.
type Case struct {
	Name        string
	Description string
	// Mode is "forced" (one standalone "zmconfigd <sections>" per cycle) or
	// "daemon" (consecutive cycles of one daemon process).
	Mode string
	// BaseLDIF and BaseLocalConfig are the inputs shared with other cases.
	BaseLDIF        []string
	BaseLocalConfig string
	Cycles          []Cycle
}

// Names lists the cases of the corpus.
func Names(tb testing.TB) []string {
	tb.Helper()

	ents, err := os.ReadDir(filepath.Join(Root(), "cases"))
	if err != nil {
		tb.Fatalf("read cases: %v", err)
	}

	var names []string

	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}

	return names
}

// Load reads a case.
func Load(tb testing.TB, name string) *Case {
	tb.Helper()

	dir := filepath.Join(Root(), "cases", name)
	kv := readKV(tb, filepath.Join(dir, "case.txt"))
	c := &Case{Name: name, Description: kv["description"], Mode: kv["mode"]}

	base := filepath.Join(Root(), "base", kv["base"])
	c.BaseLDIF = globSorted(tb, filepath.Join(base, "ldif", "*.ldif"))
	c.BaseLocalConfig = filepath.Join(base, "localconfig.xml")

	for n := 1; ; n++ {
		cd := filepath.Join(dir, fmt.Sprintf("cycle-%d", n))
		if _, err := os.Stat(cd); err != nil {
			break
		}

		cyc := Cycle{N: n, LDIF: globSorted(tb, filepath.Join(cd, "ldif", "*.ldif"))}

		if _, err := os.Stat(filepath.Join(cd, "localconfig.xml")); err == nil {
			cyc.LocalConfig = filepath.Join(cd, "localconfig.xml")
		}

		if data, err := os.ReadFile(filepath.Join(cd, "sections")); err == nil { //nolint:gosec // corpus path
			cyc.Sections = strings.Fields(string(data))
		}

		cyc.Expected = loadExpected(tb, filepath.Join(cd, "expected"))
		c.Cycles = append(c.Cycles, cyc)
	}

	if len(c.Cycles) == 0 {
		tb.Fatalf("case %s has no cycles", name)
	}

	return c
}

func readKV(tb testing.TB, file string) map[string]string {
	tb.Helper()

	f, err := os.Open(file) //nolint:gosec // corpus path
	if err != nil {
		tb.Fatalf("open %s: %v", file, err)
	}

	defer func() { _ = f.Close() }()

	kv := map[string]string{}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	return kv
}

func globSorted(tb testing.TB, pattern string) []string {
	tb.Helper()

	m, err := filepath.Glob(pattern)
	if err != nil {
		tb.Fatalf("glob %s: %v", pattern, err)
	}

	sort.Strings(m)

	return m
}

func loadExpected(tb testing.TB, dir string) Expected {
	tb.Helper()

	exp := Expected{Files: map[string][]byte{}, Modes: map[string]string{}}

	filesDir := filepath.Join(dir, "files")

	err := filepath.WalkDir(filesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(filesDir, p)

		data, rerr := os.ReadFile(p) //nolint:gosec // corpus path
		if rerr != nil {
			return rerr
		}

		exp.Files[filepath.ToSlash(rel)] = data

		return nil
	})
	if err != nil {
		tb.Fatalf("read %s: %v", filesDir, err)
	}

	if data, err := os.ReadFile(filepath.Join(dir, "modes.txt")); err == nil { //nolint:gosec // corpus path
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if p, m, ok := strings.Cut(line, " "); ok {
				exp.Modes[p] = m
			}
		}
	}

	if data, err := os.ReadFile(filepath.Join(dir, "exec.jsonl")); err == nil { //nolint:gosec // corpus path
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 0, 1<<16), 1<<24)

		for sc.Scan() {
			var argv []string
			if err := json.Unmarshal(sc.Bytes(), &argv); err != nil {
				tb.Fatalf("exec.jsonl: %v", err)
			}

			exp.Exec = append(exp.Exec, argv)
		}
	}

	return exp
}

// TreeFiles returns the vendored legacy install tree (templates, zmconfigd.cf,
// attrs.xml, ...) by path relative to the install root.
func TreeFiles(tb testing.TB) map[string][]byte {
	tb.Helper()

	root := filepath.Join(Root(), "tree")
	out := map[string][]byte{}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(root, p)

		data, rerr := os.ReadFile(p) //nolint:gosec // corpus path
		if rerr != nil {
			return rerr
		}

		out[filepath.ToSlash(rel)] = data

		return nil
	})
	if err != nil {
		tb.Fatalf("read tree: %v", err)
	}

	return out
}

// PostconfState folds the postconf invocations of a run into the final state
// they leave: key -> value, with the empty marker Unset for "postconf -X".
// The legacy run executes one postconf per key from several threads and the Go
// run batches keys, so only the resulting state is comparable, not the
// argv sequence.
func PostconfState(argvs [][]string) map[string]string {
	state := map[string]string{}

	for _, argv := range argvs {
		if len(argv) < 2 || path.Base(argv[0]) != "postconf" {
			continue
		}

		switch argv[1] {
		case "-e":
			for _, kv := range argv[2:] {
				k, v, _ := strings.Cut(kv, "=")
				state[k] = v
			}
		case "-X":
			for _, k := range argv[2:] {
				state[k] = Unset
			}
		}
	}

	return state
}

// Unset is the PostconfState value of a parameter removed by "postconf -X".
const Unset = "\x00unset"

// OtherExec returns the non-postconf commands of a run as "name args..."
// lines, in execution order.
func OtherExec(argvs [][]string) []string {
	var out []string

	for _, argv := range argvs {
		if len(argv) == 0 || path.Base(argv[0]) == "postconf" {
			continue
		}

		out = append(out, path.Base(argv[0])+" "+strings.Join(argv[1:], " "))
	}

	return out
}

// Diff describes the differences between the files the legacy run wrote and
// the files the Go run wrote, one string per difference (empty when equal).
// Both maps are keyed by path relative to the install root.
func Diff(want, got map[string][]byte) []string {
	var out []string

	for _, p := range sortedKeys(want) {
		g, ok := got[p]
		if !ok {
			out = append(out, fmt.Sprintf("%s: legacy wrote it, Go did not", p))

			continue
		}

		if !bytes.Equal(want[p], g) {
			out = append(out, fmt.Sprintf("%s: content differs\n%s", p, lineDiff(string(want[p]), string(g))))
		}
	}

	for _, p := range sortedKeys(got) {
		if _, ok := want[p]; !ok {
			out = append(out, fmt.Sprintf("%s: Go wrote it, legacy did not", p))
		}
	}

	return out
}

// lineDiff reports the first differing lines (legacy "-", Go "+").
func lineDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")

	var b strings.Builder

	shown := 0

	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}

		if i < len(gl) {
			g = gl[i]
		}

		if w == g {
			continue
		}

		shown++
		if shown > 8 {
			b.WriteString("    ...\n")

			break
		}

		b.WriteString("    line " + strconv.Itoa(i+1) + "\n      - legacy: " + w + "\n      + go:     " + g + "\n")
	}

	if shown == 0 {
		return fmt.Sprintf("    only the ends differ: legacy %d bytes ...%q, go %d bytes ...%q",
			len(want), tail(want), len(got), tail(got))
	}

	return strings.TrimRight(b.String(), "\n")
}

func tail(s string) string {
	const n = 16

	if len(s) > n {
		return s[len(s)-n:]
	}

	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// SeedLDIF returns the product's seed entries (the LDIF files its directory
// server package loads into an empty directory), in load order.
func SeedLDIF() []string {
	var out []string

	seeds := []string{
		"carbonio", "zimbra_globalconfig", "zimbra_defaultcos", "zimbra_defaultexternalcos", "mimehandlers",
	}

	for _, n := range seeds {
		out = append(out, filepath.Join(Root(), "tree", "seed", n+".ldif"))
	}

	return out
}
