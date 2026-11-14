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

// newCDSFakeController is a legacy-mode Controller over the in-memory FS with
// a JDK at /fake/java and the given directory order for the mailbox jars.
func newCDSFakeController(t *testing.T, jarsOrder []string) (*Controller, *systest.Fakes) {
	t.Helper()

	f := systest.New()
	env := f.System()
	env.FS = dirOrderFS{FS: env.FS, order: map[string][]string{"/opt/zextras/mailbox/jars": jarsOrder}}

	ctl := NewController(withMode(env, false),
		WithConfigLoader(func() (map[string]string, error) {
			return map[string]string{"mailboxd_java_home": "/fake/java"}, nil
		}),
	)
	stubReadiness(ctl)

	f.FS.Put("/fake/java/bin/java", []byte("x"), 0o755)
	f.FS.Put("/fake/java/lib/modules", []byte("modules"), 0o644)
	f.FS.Put("/fake/java/lib/server/libjvm.so", []byte("libjvm"), 0o755)

	for _, dir := range []string{
		"/fake", "/fake/java", "/fake/java/bin", "/fake/java/lib", "/fake/java/lib/server",
		ctl.p.mailbox + "/jars", ctl.p.lib + "/ext",
	} {
		f.FS.PutDir(dir, 0o755)
	}

	for _, name := range jarsOrder {
		f.FS.Put(ctl.p.mailbox+"/jars/"+name, []byte(name), 0o644)
	}

	return ctl, f
}

func TestPrepareMailboxCDS_ClasspathFileIsMailboxJarThenJarsInDirectoryOrder(t *testing.T) {
	t.Parallel()

	// Unsorted on purpose: sorting would change which of the duplicate classes
	// the JVM resolves. mailbox.jar in the middle must be listed first and
	// once; other files are not jars.
	ctl, f := newCDSFakeController(t, []string{
		"zeta.jar", "mailbox.jar", "alpha.JAR", "README.txt", "mailbox.jar.bak", "jar", "beta.jar", "nested.jar.d",
	})

	require.NoError(t, ctl.prepareMailboxCDS(context.Background(), "/fake/java/bin/java", nil))

	got, ok := f.FS.Data(ctl.p.data + "/carbonio-appserver/classpath.args")
	require.True(t, ok, "argument file written")

	j := ctl.p.mailbox + "/jars/"
	assert.Equal(t, "-cp "+j+"mailbox.jar:"+j+"zeta.jar:"+j+"alpha.JAR:"+j+"beta.jar\n", string(got))
	assert.Equal(t, 1, strings.Count(string(got), "mailbox.jar"), "mailbox.jar is listed once")
}

func TestPrepareMailboxCDS_ArgfileIsWrittenAtomically(t *testing.T) {
	t.Parallel()

	ctl, f := newCDSFakeController(t, []string{"mailbox.jar"})

	require.NoError(t, ctl.prepareMailboxCDS(context.Background(), "/fake/java/bin/java", nil))

	assert.Contains(t, f.Rec.Of("fs.write"), ctl.p.data+"/carbonio-appserver/classpath.args mode=0644",
		"the argument file goes through WriteFileAtomic")
}

func TestPrepareMailboxCDS_FailsWithoutJarsDirectory(t *testing.T) {
	t.Parallel()

	ctl, f := newCDSFakeController(t, nil)
	require.NoError(t, f.FS.RemoveAll(ctl.p.mailbox+"/jars"))

	require.Error(t, ctl.prepareMailboxCDS(context.Background(), "/fake/java/bin/java", nil))
}

func TestPrepareMailboxCDS_FailsWhenJavaCannotBeResolved(t *testing.T) {
	t.Parallel()

	ctl, _ := newCDSFakeController(t, []string{"mailbox.jar"})

	require.Error(t, ctl.prepareMailboxCDS(context.Background(), "/missing/bin/java", nil))
	require.Error(t, ctl.prepareMailboxCDS(context.Background(), "bin/java", nil), "relative path")
}

func TestStartJava_MailboxCDSCommandLine(t *testing.T) {
	t.Parallel()

	ctl, f := newCDSFakeController(t, []string{"b.jar", "mailbox.jar", "a.jar"})

	require.NoError(t, ctl.startJava(context.Background(), ctl.registry[svcMailbox]))

	starts := f.Rec.Of("exec.start")
	require.Len(t, starts, 1)

	state := ctl.p.data + "/carbonio-appserver"
	assert.Contains(t, starts[0], "-XX:SharedArchiveFile="+state+"/mailboxd.jsa -XX:+AutoCreateSharedArchive ")
	assert.Contains(t, starts[0], " @"+state+"/classpath.args "+mailboxMainClass)
	assert.True(t, f.FS.Exists(state), "state directory created")
	assert.True(t, f.FS.Exists(state+"/classpath.args"))
}

func TestStartJava_MailboxCDSPreparationFailureFallsBackToOldClasspath(t *testing.T) {
	t.Parallel()

	ctl, f := newCDSFakeController(t, []string{"mailbox.jar", "a.jar"})
	// Preparation fails: the argument file cannot be written.
	f.FS.Fail = func(op, path string) error {
		if op == "WriteFileAtomic" && strings.HasSuffix(path, "/classpath.args") {
			return os.ErrPermission
		}

		return nil
	}

	require.NoError(t, ctl.startJava(context.Background(), ctl.registry[svcMailbox]),
		"a start never fails because of the cache")

	starts := f.Rec.Of("exec.start")
	require.Len(t, starts, 1)

	assert.Contains(t, starts[0], " -cp "+ctl.p.mailbox+"/jars/mailbox.jar:"+ctl.p.mailbox+"/jars/* "+mailboxMainClass)
	assert.NotContains(t, starts[0], "SharedArchiveFile")
	assert.NotContains(t, starts[0], "AutoCreateSharedArchive")
	assert.NotContains(t, starts[0], "@")
	assert.True(t, strings.HasSuffix(starts[0], mailboxMainClass), "main class stays last")
}

func TestStartJava_MailboxFallsBackWhenJarsDirectoryMissing(t *testing.T) {
	t.Parallel()

	ctl, f := newCDSFakeController(t, nil)
	require.NoError(t, f.FS.RemoveAll(ctl.p.mailbox+"/jars"))

	require.NoError(t, ctl.startJava(context.Background(), ctl.registry[svcMailbox]))

	starts := f.Rec.Of("exec.start")
	require.Len(t, starts, 1)
	assert.Contains(t, starts[0], " -cp ")
	assert.NotContains(t, starts[0], "SharedArchiveFile")
}

func TestStartJava_MilterKeepsItsCommandLine(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	assert.Nil(t, ctl.registry[svcMilter].Java.Prepare, "only the mailbox JVM prepares class data sharing")

	args := ctl.milterJavaArgs(map[string]string{})
	assert.Contains(t, args, "-classpath")
	assert.NotContains(t, strings.Join(args, " "), "SharedArchive")
}

// cdsHost is a mailbox install on the real filesystem, to exercise what the
// in-memory FS cannot: modification times, directory symlinks, recursion.
type cdsHost struct {
	t       *testing.T
	ctl     *Controller
	root    string
	javaBin string
	archive string
	stamp   string
}

func newCDSHost(t *testing.T) *cdsHost {
	t.Helper()

	root := t.TempDir()
	ctl := newRealController(t)
	ctl.p = newPaths(root, ctl.p.run)

	h := &cdsHost{
		t:       t,
		ctl:     ctl,
		root:    root,
		javaBin: filepath.Join(root, "common/lib/jvm/java/bin/java"),
		archive: filepath.Join(ctl.p.appserverState(), cdsArchiveName),
		stamp:   filepath.Join(ctl.p.appserverState(), cdsInputsName),
	}

	h.writeJDK("jdk-1", "modules-1", "libjvm-1")
	h.link(filepath.Join(root, "common/lib/jvm/java"), filepath.Join(root, "jdk-1"))

	h.write(filepath.Join(ctl.p.mailbox, "jars/mailbox.jar"), "mailbox")
	h.write(filepath.Join(ctl.p.mailbox, "jars/other.jar"), "other")
	h.write(filepath.Join(ctl.p.lib, "ext/carbonio/ext.jar"), "ext")

	require.NoError(t, os.MkdirAll(ctl.p.appserverState(), 0o755))

	return h
}

func (h *cdsHost) write(path, content string) {
	h.t.Helper()
	require.NoError(h.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(h.t, os.WriteFile(path, []byte(content), 0o644))
}

func (h *cdsHost) writeJDK(dir, modules, libjvm string) {
	jdk := filepath.Join(h.root, dir)
	h.write(filepath.Join(jdk, "lib/modules"), modules)
	h.write(filepath.Join(jdk, "lib/server/libjvm.so"), libjvm)
	h.write(filepath.Join(jdk, "bin/java"), "java")
}

// link points name at target, replacing a previous link.
func (h *cdsHost) link(name, target string) {
	_ = os.Remove(name)

	require.NoError(h.t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(h.t, os.Symlink(target, name))
}

// prepare runs the prestart step with an archive in place and reports whether
// the archive survived it.
func (h *cdsHost) prepare() (archiveKept bool) {
	t := h.t
	t.Helper()

	require.NoError(t, os.WriteFile(h.archive, []byte("archive"), 0o644))
	require.NoError(t, h.ctl.prepareMailboxCDS(context.Background(), h.javaBin, nil))

	_, err := os.Stat(h.archive)

	return err == nil
}

func TestPrepareMailboxCDS_UnchangedInputsKeepTheArchive(t *testing.T) {
	t.Parallel()

	h := newCDSHost(t)

	assert.False(t, h.prepare(), "no stamp yet: an archive of unknown origin is dropped")
	assert.True(t, h.prepare(), "same inputs: the archive is kept")
	assert.True(t, h.prepare(), "and again")
}

func TestPrepareMailboxCDS_IrrelevantChangesKeepTheArchive(t *testing.T) {
	t.Parallel()

	h := newCDSHost(t)
	h.prepare()

	h.write(filepath.Join(h.ctl.p.mailbox, "jars/readme.txt"), "not a jar")
	h.write(filepath.Join(h.ctl.p.lib, "ext/carbonio/conf.xml"), "not a jar")
	h.write(filepath.Join(h.root, "jdk-1/lib/other"), "not tracked")

	assert.True(t, h.prepare(), "changes outside the tracked files leave the archive alone")
}

func TestPrepareMailboxCDS_ChangedInputsDeleteTheArchive(t *testing.T) {
	t.Parallel()

	later := time.Now().Add(time.Hour)

	for name, mutate := range map[string]func(h *cdsHost){
		"mailbox jar rewritten": func(h *cdsHost) {
			h.write(filepath.Join(h.ctl.p.mailbox, "jars/other.jar"), "other, longer")
		},
		"mailbox jar touched": func(h *cdsHost) {
			require.NoError(h.t, os.Chtimes(filepath.Join(h.ctl.p.mailbox, "jars/mailbox.jar"), later, later))
		},
		"mailbox jar added": func(h *cdsHost) {
			h.write(filepath.Join(h.ctl.p.mailbox, "jars/new.jar"), "new")
		},
		"mailbox jar removed": func(h *cdsHost) {
			require.NoError(h.t, os.Remove(filepath.Join(h.ctl.p.mailbox, "jars/other.jar")))
		},
		"extension jar touched": func(h *cdsHost) {
			require.NoError(h.t, os.Chtimes(filepath.Join(h.ctl.p.lib, "ext/carbonio/ext.jar"), later, later))
		},
		"extension jar added in a subdirectory": func(h *cdsHost) {
			h.write(filepath.Join(h.ctl.p.lib, "ext/other/deep/ext2.jar"), "ext2")
		},
		"JDK runtime image touched": func(h *cdsHost) {
			require.NoError(h.t, os.Chtimes(filepath.Join(h.root, "jdk-1/lib/modules"), later, later))
		},
		"JDK VM library replaced": func(h *cdsHost) {
			h.write(filepath.Join(h.root, "jdk-1/lib/server/libjvm.so"), "libjvm-2, longer")
		},
		"java switched to another JDK": func(h *cdsHost) {
			h.writeJDK("jdk-2", "modules-1", "libjvm-1")
			h.link(filepath.Join(h.root, "common/lib/jvm/java"), filepath.Join(h.root, "jdk-2"))
			require.NoError(h.t, os.Chtimes(filepath.Join(h.root, "jdk-2/lib/modules"), later, later))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newCDSHost(t)
			h.prepare()
			require.True(t, h.prepare(), "precondition: archive kept while nothing changed")

			mutate(h)

			assert.False(t, h.prepare(), "the stale archive is deleted")
			assert.True(t, h.prepare(), "the new fingerprint was stored")
		})
	}
}

func TestResolveSymlinks_FollowsDirectoryAndFileLinks(t *testing.T) {
	t.Parallel()

	h := newCDSHost(t)

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

	h := newCDSHost(t)
	h.link(filepath.Join(h.root, "loop-a"), filepath.Join(h.root, "loop-b"))
	h.link(filepath.Join(h.root, "loop-b"), filepath.Join(h.root, "loop-a"))

	_, err := h.ctl.resolveSymlinks(filepath.Join(h.root, "loop-a"))
	require.Error(t, err)
}
