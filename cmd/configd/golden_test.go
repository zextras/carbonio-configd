// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/test/golden"
)

// installRoot is where the legacy corpus was generated; the Go run uses a
// temporary root instead and its paths are mapped back for the comparison.
const (
	installRoot = "/opt/zextras"
	// legacyLDAPAddr is the directory address of the legacy run; the Go run
	// listens on a free port and its output is mapped back.
	legacyLDAPAddr = "127.0.0.1:389"
)

// TestGoldenParity runs every case of test/golden through the production
// wiring (buildRuntime, runForcedMode or the daemon's per-cycle phases) with
// the in-memory system and an LDAP directory loaded from the same LDIF the
// legacy run read, and compares the result with what the legacy zmconfigd and
// ProxyConfGen produced.
func TestGoldenParity(t *testing.T) {
	for _, name := range golden.Names(t) {
		t.Run(name, func(t *testing.T) {
			runGoldenCase(t, golden.Load(t, name))
		})
	}
}

// goldenRun is the Go side of one case.
type goldenRun struct {
	t      *testing.T
	c      *golden.Case
	base   string
	dir    *ldapfake.Directory
	srv    *ldapfake.Server
	fakes  *systest.Fakes
	tree   map[string][]byte
	opsLen int
}

func runGoldenCase(t *testing.T, c *golden.Case) {
	t.Helper()

	base := t.TempDir()
	t.Setenv(config.BaseDirEnv, base)

	g := &goldenRun{t: t, c: c, base: base, dir: ldapfake.NewDirectory(), fakes: systest.New()}
	// The legacy container has no systemd: restarts go through the zm*ctl
	// scripts, and the Go orchestration probe finds no enabled target.
	g.fakes.Exec.OnCmdline("systemctl is-enabled", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{ExitCode: 1}, &sys.ExitError{Code: 1}
	})
	g.srv = ldapfake.New(t, ldapfake.Options{Directory: g.dir})

	g.installTree()

	for _, f := range golden.SeedLDIF() {
		g.applyLDIF(f)
	}

	for _, f := range c.BaseLDIF {
		g.applyLDIF(f)
	}

	var rt *runtime

	for _, cyc := range c.Cycles {
		for _, f := range cyc.LDIF {
			g.applyLDIF(f)
		}

		switch {
		case cyc.N == 1:
			g.installLocalConfig(c.BaseLocalConfig)

			fallthrough
		case cyc.LocalConfig != "":
			if cyc.LocalConfig != "" {
				g.installLocalConfig(cyc.LocalConfig)
			}
		}

		g.fakes.Rec.Reset()

		ctx := context.Background()

		if c.Mode == "forced" || rt == nil {
			var err error

			rt, err = buildRuntime(ctx, g.fakes.System(), &runtimeOpts{})
			if err != nil {
				t.Fatalf("buildRuntime: %v", err)
			}
		}

		if c.Mode == "forced" {
			if code := runForcedMode(ctx, &Args{ForcedConfigs: cyc.Sections}, rt); code != 0 {
				t.Fatalf("cycle %d: forced run exited %d", cyc.N, code)
			}
		} else {
			g.daemonCycle(ctx, rt)
		}

		g.compare(cyc)
	}
}

// daemonCycle runs the phases of one daemon iteration (load, parse, plan,
// rewrite, restarts) on the runtime that persists across cycles.
func (g *goldenRun) daemonCycle(ctx context.Context, rt *runtime) {
	t := g.t

	if _, _, err := runLoadAndParse(ctx, rt.cm, rt.cfg); err != nil {
		t.Fatalf("load and parse: %v", err)
	}

	buildServiceDependencies(ctx, rt.sm, rt.state)

	pl, err := rt.cm.Plan(ctx)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if err := rt.cm.Execute(ctx, pl); err != nil {
		t.Fatalf("execute: %v", err)
	}

	runRestartsPhase(ctx, rt.cm, pl)
	rt.state.SetFirstRun(false)
}

func (g *goldenRun) applyLDIF(file string) {
	g.t.Helper()

	f, err := os.Open(file)
	if err != nil {
		g.t.Fatalf("open %s: %v", file, err)
	}

	defer func() { _ = f.Close() }()

	if err := g.dir.ApplyLDIF(f); err != nil {
		g.t.Fatalf("%s: %v", file, err)
	}
}

// installTree puts the vendored legacy install tree into the in-memory file
// system under the temporary root, and the mailbox jar whose BuildInfo the
// proxy generator reads from the real one.
func (g *goldenRun) installTree() {
	g.t.Helper()

	g.tree = golden.TreeFiles(g.t)

	for rel, data := range g.tree {
		if strings.HasPrefix(rel, "seed/") || strings.HasPrefix(rel, "mailbox/") {
			continue
		}

		// WalkDir only sees directories that exist as entries.
		for d := filepath.Dir(filepath.Join(g.base, rel)); d != "/" && !g.fakes.FS.Exists(d); d = filepath.Dir(d) {
			g.fakes.FS.PutDir(d, 0o755)
		}

		g.fakes.FS.Put(filepath.Join(g.base, rel), data, 0o644)
	}

	// The zmconfigd.cf parser and the BuildInfo reader open real files.
	cf := filepath.Join(g.base, "conf", "zmconfigd.cf")
	if err := os.MkdirAll(filepath.Dir(cf), 0o755); err != nil {
		g.t.Fatal(err)
	}

	if err := os.WriteFile(cf, g.tree["conf/zmconfigd.cf"], 0o644); err != nil {
		g.t.Fatal(err)
	}

	jar := filepath.Join(g.base, "mailbox", "jars", "mailbox.jar")
	if err := os.MkdirAll(filepath.Dir(jar), 0o755); err != nil {
		g.t.Fatal(err)
	}

	f, err := os.Create(jar)
	if err != nil {
		g.t.Fatal(err)
	}

	zw := zip.NewWriter(f)

	w, err := zw.Create("com/zimbra/cs/util/buildInfo.properties")
	if err != nil {
		g.t.Fatal(err)
	}

	if _, err := w.Write(g.tree["mailbox/buildInfo.properties"]); err != nil {
		g.t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		g.t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		g.t.Fatal(err)
	}
}

// installLocalConfig writes localconfig.xml (the localconfig loader reads the
// real file system) with the directory URL pointing at the in-process server.
func (g *goldenRun) installLocalConfig(src string) {
	g.t.Helper()

	data, err := os.ReadFile(src)
	if err != nil {
		g.t.Fatal(err)
	}

	data = []byte(strings.ReplaceAll(string(data), legacyLDAPAddr, g.srv.Addr()))

	dst := filepath.Join(g.base, "conf", "localconfig.xml")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		g.t.Fatal(err)
	}

	if err := os.WriteFile(dst, data, 0o600); err != nil {
		g.t.Fatal(err)
	}
}

// written returns the files the run created or replaced, by path relative to
// the install root, with the temporary root mapped back to /opt/zextras.
func (g *goldenRun) written() (files map[string][]byte, modes map[string]string) {
	files, modes = map[string][]byte{}, map[string]string{}

	seen := map[string]bool{}

	for _, op := range g.fakes.Rec.Ops() {
		if !strings.HasPrefix(op.Kind, "fs.") {
			continue
		}

		_, target, ok := strings.Cut(op.Detail, " -> ")
		if !ok {
			target, _, _ = strings.Cut(op.Detail, " ")
		}

		if strings.HasPrefix(target, g.base+"/") {
			seen[strings.TrimPrefix(target, g.base+"/")] = true
		}
	}

	for rel := range seen {
		data, ok := g.fakes.FS.Data(filepath.Join(g.base, rel))
		if !ok {
			continue
		}

		files[rel] = []byte(strings.NewReplacer(g.base, installRoot, g.srv.Addr(), legacyLDAPAddr).Replace(string(data)))

		if m, ok := g.fakes.FS.Mode(filepath.Join(g.base, rel)); ok {
			modes[rel] = strconvOctal(uint32(m.Perm()))
		}
	}

	return files, modes
}

func strconvOctal(v uint32) string {
	const digits = "01234567"

	if v == 0 {
		return "0"
	}

	var b []byte

	for ; v > 0; v >>= 3 {
		b = append([]byte{digits[v&7]}, b...)
	}

	return string(b)
}

// compare checks one cycle against the legacy output.
func (g *goldenRun) compare(cyc golden.Cycle) {
	t := g.t
	t.Helper()

	files, modes := g.written()

	for _, d := range golden.Diff(cyc.Expected.Files, files) {
		t.Errorf("cycle %d: %s", cyc.N, d)
	}

	for _, p := range sortedMapKeys(cyc.Expected.Modes) {
		if got, ok := modes[p]; ok && got != cyc.Expected.Modes[p] {
			t.Errorf("cycle %d: %s mode: legacy %s, go %s", cyc.N, p, cyc.Expected.Modes[p], got)
		}
	}

	var got [][]string

	for _, c := range g.fakes.Exec.Calls() {
		// A read-only probe of the orchestration mode: legacy has none.
		if c.Path == "systemctl" && len(c.Args) > 0 && c.Args[0] == "is-enabled" {
			continue
		}

		got = append(got, c.Argv())
	}

	wantPC, gotPC := golden.PostconfState(cyc.Expected.Exec), golden.PostconfState(got)
	for _, k := range sortedMapKeys(wantPC) {
		if gotPC[k] != wantPC[k] {
			t.Errorf("cycle %d: postconf %s: legacy %q, go %q", cyc.N, k, wantPC[k], gotPC[k])
		}
	}

	for _, k := range sortedMapKeys(gotPC) {
		if _, ok := wantPC[k]; !ok {
			t.Errorf("cycle %d: postconf %s: go sets %q, legacy does not touch it", cyc.N, k, gotPC[k])
		}
	}

	wantOther, gotOther := golden.OtherExec(cyc.Expected.Exec), golden.OtherExec(got)
	if strings.Join(wantOther, "\n") != strings.Join(gotOther, "\n") {
		t.Errorf("cycle %d: commands differ\nlegacy:\n  %s\ngo:\n  %s", cyc.N,
			strings.Join(wantOther, "\n  "), strings.Join(gotOther, "\n  "))
	}
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
