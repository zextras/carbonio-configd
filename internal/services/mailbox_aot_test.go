// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// dirOrderFS lists the directories in order with the order readdir would,
// which the in-memory FS has not: unlisted directories keep its (sorted) one.
type dirOrderFS struct {
	sys.FS

	order map[string][]string
}

func (d dirOrderFS) ReadDirNames(name string) ([]string, error) {
	names, err := d.FS.ReadDirNames(name)
	if err != nil {
		return nil, err
	}

	if order, ok := d.order[name]; ok {
		return order, nil
	}

	return names, nil
}

const fakeJava = "/fake/java/bin/java"

// aotFake is a legacy-mode Controller over the in-memory FS with a JDK at
// /fake/java.
type aotFake struct {
	ctl   *Controller
	f     *systest.Fakes
	state string
}

// newAOTFake builds the install: JDK feature version jdk, the mailbox jars and
// the extension jars (nil: no extension directory) listed in the given order.
func newAOTFake(t *testing.T, jdk string, jarsOrder, extOrder []string) *aotFake {
	t.Helper()

	f := systest.New()
	env := f.System()
	env.FS = dirOrderFS{FS: env.FS, order: map[string][]string{
		"/opt/zextras/mailbox/jars":     jarsOrder,
		"/opt/zextras/lib/ext/carbonio": extOrder,
	}}

	ctl := NewController(withMode(env, false),
		WithConfigLoader(func() (map[string]string, error) {
			return map[string]string{"mailboxd_java_home": "/fake/java"}, nil
		}),
	)
	stubReadiness(ctl)

	f.FS.Put(fakeJava, []byte("x"), 0o755)
	f.FS.Put("/fake/java/lib/modules", []byte("modules"), 0o644)
	f.FS.Put("/fake/java/lib/server/libjvm.so", []byte("libjvm"), 0o755)
	f.FS.Put("/fake/java/release", []byte("JAVA_VERSION=\""+jdk+"\"\nIMPLEMENTOR=\"x\"\n"), 0o644)

	dirs := []string{
		"/fake", "/fake/java", "/fake/java/bin", "/fake/java/lib", "/fake/java/lib/server",
		ctl.p.mailbox + "/jars", ctl.p.lib + "/ext",
	}
	if extOrder != nil {
		dirs = append(dirs, ctl.p.lib+"/ext/carbonio")
	}

	for _, dir := range dirs {
		f.FS.PutDir(dir, 0o755)
	}

	for _, name := range jarsOrder {
		f.FS.Put(ctl.p.mailbox+"/jars/"+name, []byte(name), 0o644)
	}

	for _, name := range extOrder {
		f.FS.Put(ctl.p.lib+"/ext/carbonio/"+name, []byte(name), 0o644)
	}

	return &aotFake{ctl: ctl, f: f, state: ctl.p.data + "/carbonio-appserver"}
}

func (a *aotFake) prepare(t *testing.T) []string {
	t.Helper()

	require.NoError(t, a.ctl.prepareMailboxAOT(context.Background(), fakeJava, nil))

	return a.argLines(t)
}

func (a *aotFake) argLines(t *testing.T) []string {
	t.Helper()

	got, ok := a.f.FS.Data(a.state + "/mailboxd.args")
	require.True(t, ok, "argument file written")

	return strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
}

func hasPrefixLine(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}

	return false
}

func TestPrepareMailboxAOT_ClasspathIsMailboxJarThenJarsThenExtensionsInDirectoryOrder(t *testing.T) {
	t.Parallel()

	// Unsorted on purpose: sorting would change which of the duplicate classes
	// the JVM resolves. mailbox.jar in the middle must be listed first and
	// once; other files are not jars.
	a := newAOTFake(t, "21.0.5",
		[]string{"zeta.jar", "mailbox.jar", "alpha.JAR", "README.txt", "mailbox.jar.bak", "jar", "beta.jar", "nested.jar.d"},
		[]string{"z-ext.jar", "a-ext.jar", "notes.txt"})

	lines := a.prepare(t)

	j := a.ctl.p.mailbox + "/jars/"
	e := a.ctl.p.lib + "/ext/carbonio/"
	cp := "-cp " + j + "mailbox.jar:" + j + "zeta.jar:" + j + "alpha.JAR:" + j + "beta.jar:" + e + "z-ext.jar:" + e + "a-ext.jar"
	assert.Contains(t, lines, cp)
	assert.Equal(t, 1, strings.Count(cp, "mailbox.jar"), "mailbox.jar is listed once")
	assert.Equal(t, "-Dzal.flatClasspath=true", lines[0])
}

func TestPrepareMailboxAOT_MissingExtensionDirectoryIsMailboxJarsOnly(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", []string{"b.jar", "mailbox.jar", "a.jar"}, nil)

	lines := a.prepare(t)

	j := a.ctl.p.mailbox + "/jars/"
	assert.Contains(t, lines, "-cp "+j+"mailbox.jar:"+j+"b.jar:"+j+"a.jar")
	assert.Contains(t, lines, "-Dzal.flatClasspath=true")
	assert.True(t, hasPrefixLine(lines, "-XX:AOTCacheOutput="), "AOT still applies without Advanced")
}

func TestPrepareMailboxAOT_JDK21HasNoAOTFlags(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "21.0.5", []string{"mailbox.jar"}, []string{"ext.jar"})

	lines := a.prepare(t)

	assert.Contains(t, lines, "-Dzal.flatClasspath=true", "the flat class path is used on every JDK")
	assert.False(t, hasPrefixLine(lines, "-XX:"), "JDK 21 rejects the AOT options: %v", lines)
	assert.False(t, hasPrefixLine(lines, "-Xlog"), "%v", lines)
	assert.False(t, a.f.FS.Exists(a.state+"/mailboxd.aot.inputs"), "no cache, no fingerprint")
}

func TestPrepareMailboxAOT_JDKVersionForms(t *testing.T) {
	t.Parallel()

	for version, aot := range map[string]bool{
		"1.8.0_402": false, "17.0.13": false, "21": false, "21.0.5": false,
		"25": true, "25.0.1": true, "25-ea": true, "26.0.1": true,
	} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			a := newAOTFake(t, version, []string{"mailbox.jar"}, nil)

			assert.Equal(t, aot, hasPrefixLine(a.prepare(t), "-XX:AOTCache"))
		})
	}
}

func TestPrepareMailboxAOT_JDK25WithoutCacheTrains(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", []string{"mailbox.jar"}, []string{"ext.jar"})

	lines := a.prepare(t)

	assert.Contains(t, lines, "-XX:AOTCacheOutput="+a.state+"/mailboxd.aot")
	assert.False(t, hasPrefixLine(lines, "-XX:AOTCache="), "%v", lines)
	assert.Contains(t, lines, "-Xlog:aot=warning:file="+a.state+"/aot.log", "AOT logging goes to a file only")
	assert.True(t, a.f.FS.Exists(a.state+"/mailboxd.aot.inputs"), "fingerprint recorded")
}

func TestPrepareMailboxAOT_JDK25WithValidCacheUsesIt(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", []string{"mailbox.jar"}, []string{"ext.jar"})
	a.prepare(t)

	// The training run wrote the cache.
	a.f.FS.Put(a.state+"/mailboxd.aot", []byte("cache"), 0o644)

	lines := a.prepare(t)

	assert.Contains(t, lines, "-XX:AOTCache="+a.state+"/mailboxd.aot")
	assert.False(t, hasPrefixLine(lines, "-XX:AOTCacheOutput"), "%v", lines)
	assert.Contains(t, lines, "-Xlog:aot=warning:file="+a.state+"/aot.log")
	assert.True(t, a.f.FS.Exists(a.state+"/mailboxd.aot"), "the cache is kept")
}

func TestPrepareMailboxAOT_FingerprintChangeDeletesTheCache(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(a *aotFake){
		"mailbox jar rewritten": func(a *aotFake) {
			a.f.FS.Put(a.ctl.p.mailbox+"/jars/other.jar", []byte("other, longer"), 0o644)
		},
		"extension jar added": func(a *aotFake) {
			a.f.FS.Put(a.ctl.p.lib+"/ext/carbonio/new.jar", []byte("new"), 0o644)
		},
		"JDK runtime image replaced": func(a *aotFake) {
			a.f.FS.Put("/fake/java/lib/modules", []byte("modules, longer"), 0o644)
		},
		"JDK VM library replaced": func(a *aotFake) {
			a.f.FS.Put("/fake/java/lib/server/libjvm.so", []byte("libjvm, longer"), 0o755)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			a := newAOTFake(t, "25.0.1", []string{"mailbox.jar", "other.jar"}, []string{"ext.jar"})
			a.prepare(t)
			a.f.FS.Put(a.state+"/mailboxd.aot", []byte("cache"), 0o644)
			require.True(t, hasPrefixLine(a.prepare(t), "-XX:AOTCache="), "precondition: valid while nothing changed")

			mutate(a)

			lines := a.prepare(t)

			assert.False(t, a.f.FS.Exists(a.state+"/mailboxd.aot"), "the stale cache is deleted")
			assert.True(t, hasPrefixLine(lines, "-XX:AOTCacheOutput="), "and retrained: %v", lines)
		})
	}
}

func TestPrepareMailboxAOT_ArgfileIsWrittenAtomically(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", []string{"mailbox.jar"}, nil)
	a.prepare(t)

	assert.Contains(t, a.f.Rec.Of("fs.write"), a.state+"/mailboxd.args mode=0644",
		"the argument file goes through WriteFileAtomic")
}

func TestPrepareMailboxAOT_Failures(t *testing.T) {
	t.Parallel()

	t.Run("no jars directory", func(t *testing.T) {
		t.Parallel()

		a := newAOTFake(t, "25.0.1", nil, nil)
		require.NoError(t, a.f.FS.RemoveAll(a.ctl.p.mailbox+"/jars"))

		require.Error(t, a.ctl.prepareMailboxAOT(context.Background(), fakeJava, nil))
	})

	t.Run("java cannot be resolved", func(t *testing.T) {
		t.Parallel()

		a := newAOTFake(t, "25.0.1", []string{"mailbox.jar"}, nil)

		require.Error(t, a.ctl.prepareMailboxAOT(context.Background(), "/missing/bin/java", nil))
		require.Error(t, a.ctl.prepareMailboxAOT(context.Background(), "bin/java", nil), "relative path")
	})

	t.Run("no release file", func(t *testing.T) {
		t.Parallel()

		a := newAOTFake(t, "25.0.1", []string{"mailbox.jar"}, nil)
		require.NoError(t, a.f.FS.Remove("/fake/java/release"))

		require.Error(t, a.ctl.prepareMailboxAOT(context.Background(), fakeJava, nil))
	})

	t.Run("unparsable version", func(t *testing.T) {
		t.Parallel()

		a := newAOTFake(t, "x", []string{"mailbox.jar"}, nil)

		require.Error(t, a.ctl.prepareMailboxAOT(context.Background(), fakeJava, nil))
	})
}

func TestStartJava_MailboxCommandLineJDK25(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", []string{"b.jar", "mailbox.jar", "a.jar"}, []string{"ext.jar"})

	require.NoError(t, a.ctl.startJava(context.Background(), a.ctl.registry[svcMailbox]))

	starts := a.f.Rec.Of("exec.start")
	require.Len(t, starts, 1)

	assert.Contains(t, starts[0], " @"+a.state+"/mailboxd.args "+mailboxMainClass)
	assert.True(t, strings.HasSuffix(starts[0], mailboxMainClass), "main class stays last")
	assert.True(t, a.f.FS.Exists(a.state), "state directory created")
	assert.True(t, a.f.FS.Exists(a.state+"/mailboxd.args"))
	assert.True(t, hasPrefixLine(a.argLines(t), "-XX:AOTCacheOutput="))
}

func TestStartJava_MailboxPreparationFailureFallsBackToOldClasspath(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", []string{"mailbox.jar", "a.jar"}, []string{"ext.jar"})
	// Preparation fails: the argument file cannot be written.
	a.f.FS.Fail = func(op, path string) error {
		if op == "WriteFileAtomic" && strings.HasSuffix(path, "/mailboxd.args") {
			return os.ErrPermission
		}

		return nil
	}

	require.NoError(t, a.ctl.startJava(context.Background(), a.ctl.registry[svcMailbox]),
		"a start never fails because of the cache")

	starts := a.f.Rec.Of("exec.start")
	require.Len(t, starts, 1)

	jars := a.ctl.p.mailbox + "/jars/"
	assert.Contains(t, starts[0], " -cp "+jars+"mailbox.jar:"+jars+"* "+mailboxMainClass)
	assert.NotContains(t, starts[0], "AOT")
	assert.NotContains(t, starts[0], "flatClasspath")
	assert.NotContains(t, starts[0], "@")
	assert.True(t, strings.HasSuffix(starts[0], mailboxMainClass), "main class stays last")
}

func TestStartJava_MailboxFallsBackWhenJarsDirectoryMissing(t *testing.T) {
	t.Parallel()

	a := newAOTFake(t, "25.0.1", nil, nil)
	require.NoError(t, a.f.FS.RemoveAll(a.ctl.p.mailbox+"/jars"))

	require.NoError(t, a.ctl.startJava(context.Background(), a.ctl.registry[svcMailbox]))

	starts := a.f.Rec.Of("exec.start")
	require.Len(t, starts, 1)
	assert.Contains(t, starts[0], " -cp ")
	assert.NotContains(t, starts[0], "AOT")
}

func TestStartJava_MilterKeepsItsCommandLine(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	assert.Nil(t, ctl.registry[svcMilter].Java.Prepare, "only the mailbox JVM prepares the AOT cache")

	args := ctl.milterJavaArgs(map[string]string{})
	assert.Contains(t, args, "-classpath")
	assert.NotContains(t, strings.Join(args, " "), "AOT")
}

// aotHost is a mailbox install on the real filesystem, to exercise what the
// in-memory FS cannot: modification times, directory symlinks, recursion.
type aotHost struct {
	t       *testing.T
	ctl     *Controller
	root    string
	javaBin string
	cache   string
	stamp   string
}

func newAOTHost(t *testing.T) *aotHost {
	t.Helper()

	root := t.TempDir()
	ctl := newRealController(t)
	ctl.p = newPaths(root, ctl.p.run)

	h := &aotHost{
		t:       t,
		ctl:     ctl,
		root:    root,
		javaBin: filepath.Join(root, "common/lib/jvm/java/bin/java"),
		cache:   filepath.Join(ctl.p.appserverState(), aotCacheName),
		stamp:   filepath.Join(ctl.p.appserverState(), aotInputsName),
	}

	h.writeJDK("jdk-1", "modules-1", "libjvm-1")
	h.link(filepath.Join(root, "common/lib/jvm/java"), filepath.Join(root, "jdk-1"))

	h.write(filepath.Join(ctl.p.mailbox, "jars/mailbox.jar"), "mailbox")
	h.write(filepath.Join(ctl.p.mailbox, "jars/other.jar"), "other")
	h.write(filepath.Join(ctl.p.lib, "ext/carbonio/ext.jar"), "ext")

	require.NoError(t, os.MkdirAll(ctl.p.appserverState(), 0o755))

	return h
}

func (h *aotHost) write(path, content string) {
	h.t.Helper()
	require.NoError(h.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(h.t, os.WriteFile(path, []byte(content), 0o644))
}

func (h *aotHost) writeJDK(dir, modules, libjvm string) {
	jdk := filepath.Join(h.root, dir)
	h.write(filepath.Join(jdk, "lib/modules"), modules)
	h.write(filepath.Join(jdk, "lib/server/libjvm.so"), libjvm)
	h.write(filepath.Join(jdk, "bin/java"), "java")
	h.write(filepath.Join(jdk, "release"), "JAVA_VERSION=\"25.0.1\"\n")
}

// link points name at target, replacing a previous link.
func (h *aotHost) link(name, target string) {
	_ = os.Remove(name)

	require.NoError(h.t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(h.t, os.Symlink(target, name))
}

// prepare runs the prestart step with a cache in place and reports whether
// the cache survived it.
func (h *aotHost) prepare() (cacheKept bool) {
	t := h.t
	t.Helper()

	require.NoError(t, os.WriteFile(h.cache, []byte("cache"), 0o644))
	require.NoError(t, h.ctl.prepareMailboxAOT(context.Background(), h.javaBin, nil))

	_, err := os.Stat(h.cache)

	return err == nil
}

func TestPrepareMailboxAOT_UnchangedInputsKeepTheCache(t *testing.T) {
	t.Parallel()

	h := newAOTHost(t)

	assert.False(t, h.prepare(), "no stamp yet: a cache of unknown origin is dropped")
	assert.True(t, h.prepare(), "same inputs: the cache is kept")
	assert.True(t, h.prepare(), "and again")
}

func TestPrepareMailboxAOT_IrrelevantChangesKeepTheCache(t *testing.T) {
	t.Parallel()

	h := newAOTHost(t)
	h.prepare()

	h.write(filepath.Join(h.ctl.p.mailbox, "jars/readme.txt"), "not a jar")
	h.write(filepath.Join(h.ctl.p.lib, "ext/carbonio/conf.xml"), "not a jar")
	h.write(filepath.Join(h.root, "jdk-1/lib/other"), "not tracked")

	assert.True(t, h.prepare(), "changes outside the tracked files leave the cache alone")
}

func TestPrepareMailboxAOT_ChangedInputsDeleteTheCache(t *testing.T) {
	t.Parallel()

	later := time.Now().Add(time.Hour)

	for name, mutate := range map[string]func(h *aotHost){
		"mailbox jar rewritten": func(h *aotHost) {
			h.write(filepath.Join(h.ctl.p.mailbox, "jars/other.jar"), "other, longer")
		},
		"mailbox jar touched": func(h *aotHost) {
			require.NoError(h.t, os.Chtimes(filepath.Join(h.ctl.p.mailbox, "jars/mailbox.jar"), later, later))
		},
		"mailbox jar added": func(h *aotHost) {
			h.write(filepath.Join(h.ctl.p.mailbox, "jars/new.jar"), "new")
		},
		"mailbox jar removed": func(h *aotHost) {
			require.NoError(h.t, os.Remove(filepath.Join(h.ctl.p.mailbox, "jars/other.jar")))
		},
		"extension jar touched": func(h *aotHost) {
			require.NoError(h.t, os.Chtimes(filepath.Join(h.ctl.p.lib, "ext/carbonio/ext.jar"), later, later))
		},
		"extension jar added in a subdirectory": func(h *aotHost) {
			h.write(filepath.Join(h.ctl.p.lib, "ext/other/deep/ext2.jar"), "ext2")
		},
		"JDK runtime image touched": func(h *aotHost) {
			require.NoError(h.t, os.Chtimes(filepath.Join(h.root, "jdk-1/lib/modules"), later, later))
		},
		"JDK VM library replaced": func(h *aotHost) {
			h.write(filepath.Join(h.root, "jdk-1/lib/server/libjvm.so"), "libjvm-2, longer")
		},
		"java switched to another JDK": func(h *aotHost) {
			h.writeJDK("jdk-2", "modules-1", "libjvm-1")
			h.link(filepath.Join(h.root, "common/lib/jvm/java"), filepath.Join(h.root, "jdk-2"))
			require.NoError(h.t, os.Chtimes(filepath.Join(h.root, "jdk-2/lib/modules"), later, later))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newAOTHost(t)
			h.prepare()
			require.True(t, h.prepare(), "precondition: cache kept while nothing changed")

			mutate(h)

			assert.False(t, h.prepare(), "the stale cache is deleted")
			assert.True(t, h.prepare(), "the new fingerprint was stored")
		})
	}
}

func TestResolveSymlinks_FollowsDirectoryAndFileLinks(t *testing.T) {
	t.Parallel()

	h := newAOTHost(t)

	// common/lib/jvm/java (dir link) -> jdk-1; add a file link and a relative one.
	h.link(filepath.Join(h.root, "jdk-1/bin/java-alias"), "java")
	h.link(filepath.Join(h.root, "rel"), "jdk-1/bin")

	for in, want := range map[string]string{
		h.javaBin:                                     filepath.Join(h.root, "jdk-1/bin/java"),
		filepath.Join(h.root, "rel/java"):             filepath.Join(h.root, "jdk-1/bin/java"),
		h.root + "/rel/../bin":                        filepath.Join(h.root, "jdk-1/bin"),
		filepath.Join(h.root, "jdk-1/bin/java-alias"): filepath.Join(h.root, "jdk-1/bin/java"),
	} {
		got, err := h.ctl.resolveSymlinks(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}

func TestResolveSymlinks_LoopFails(t *testing.T) {
	t.Parallel()

	h := newAOTHost(t)
	h.link(filepath.Join(h.root, "loop-a"), filepath.Join(h.root, "loop-b"))
	h.link(filepath.Join(h.root, "loop-b"), filepath.Join(h.root, "loop-a"))

	_, err := h.ctl.resolveSymlinks(filepath.Join(h.root, "loop-a"))
	require.Error(t, err)
}
