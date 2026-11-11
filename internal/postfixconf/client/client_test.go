// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package client

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/postfixconf"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	base    = "/opt/zextras"
	helper  = base + "/libexec/carbonio-postfix-conf"
	staging = base + "/data/postfix/conf-staging"
)

func TestTriggerSelection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		uid     int
		systemd func() bool
		want    string
	}{
		{"systemd mode", 1000, func() bool { return true }, "/usr/bin/systemctl start carbonio-postfix-conf.service"},
		{"systemd mode wins over root", 0, func() bool { return true }, "/usr/bin/systemctl start carbonio-postfix-conf.service"},
		{"root, no systemd", 0, func() bool { return false }, helper + " apply"},
		{"root, nil mode", 0, nil, helper + " apply"},
		{"non-root legacy", 1000, func() bool { return false }, "/usr/bin/sudo -n " + helper + " apply"},
		{"non-root nil mode", 999, nil, "/usr/bin/sudo -n " + helper + " apply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := systest.New()
			f.Proc.UserID = tc.uid

			c := NewClient(base, f.System(), tc.systemd)

			err := c.Apply(context.Background(), postfixconf.Change{Set: []postfixconf.KV{{Key: "relayhost", Value: "b c"}}})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			if got := f.Exec.Lines(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("commands = %q, want %q", got, tc.want)
			}

			if data, ok := f.FS.Data(staging + "/postconf.set"); !ok || string(data) != "relayhost=b c\n" {
				t.Errorf("staged = %q, %v", data, ok)
			}

			if mode, _ := f.FS.Mode(staging + "/postconf.set"); mode.Perm() != 0o600 {
				t.Errorf("staged mode = %v, want 0600", mode)
			}
		})
	}
}

func TestApplyStagesAllKinds(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.UserID = 0

	err := NewClient(base, f.System(), nil).Apply(context.Background(), postfixconf.Change{
		Files: map[string][]byte{postfixconf.MasterCf: []byte("smtp inet n - n - - smtpd\n")},
		Unset: []string{"smtpd_banner", "relayhost"},
		Set:   []postfixconf.KV{{Key: "relayhost", Value: "1"}, {Key: "smtpd_banner", Value: ""}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"master.cf":      "smtp inet n - n - - smtpd\n",
		"postconf.unset": "smtpd_banner\nrelayhost\n",
		"postconf.set":   "relayhost=1\nsmtpd_banner=\n",
	} {
		if got, _ := f.FS.Data(staging + "/" + name); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	if n := len(f.Exec.Calls()); n != 1 {
		t.Errorf("one trigger per Apply, got %d", n)
	}
}

func TestApplyRejectsBeforeStaging(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.UserID = 0

	err := NewClient(base, f.System(), nil).Apply(context.Background(), postfixconf.Change{
		Set: []postfixconf.KV{{Key: "daemon_directory", Value: "/tmp"}},
	})
	if !errors.Is(err, postfixconf.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}

	if len(f.Rec.Strings()) != 0 {
		t.Errorf("a rejected change must touch nothing, recorded %q", f.Rec.Strings())
	}
}

func TestApplyEmptyIsNoop(t *testing.T) {
	t.Parallel()

	f := systest.New()
	if err := NewClient(base, f.System(), nil).Apply(context.Background(), postfixconf.Change{}); err != nil {
		t.Fatal(err)
	}

	if len(f.Rec.Strings()) != 0 {
		t.Errorf("recorded %q", f.Rec.Strings())
	}
}

func TestTriggerFailureIsLoudAndClearsStaging(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.UserID = 999
	f.Exec.OnPath("/usr/bin/sudo", systest.Fail(1, "sudo: a password is required"))

	err := NewClient(base, f.System(), nil).Apply(context.Background(),
		postfixconf.Change{Set: []postfixconf.KV{{Key: "relayhost", Value: "b"}}})
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, want := range []string{helper, "password is required", "sudo -n"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}

	if _, ok := f.FS.Data(staging + "/postconf.set"); ok {
		t.Error("failed apply must not leave staged files")
	}

	if got := f.Exec.Lines(); len(got) != 1 {
		t.Errorf("no fallback command may run, got %q", got)
	}
}

func TestStaleStagingIsDiscarded(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.UserID = 0
	f.FS.PutDir(staging, 0o700)
	f.FS.Put(staging+"/master.cf", []byte("stale"), 0o600)

	if err := NewClient(base, f.System(), nil).Apply(context.Background(),
		postfixconf.Change{Unset: []string{"relayhost"}}); err != nil {
		t.Fatal(err)
	}

	if f.FS.Exists(staging + "/master.cf") {
		t.Error("stale staged master.cf must not ride along with a later change")
	}
}

func TestIsConfPath(t *testing.T) {
	t.Parallel()

	c := NewClient(base, systest.New().System(), nil)

	for path, want := range map[string]bool{
		"common/conf/master.cf":                true,
		base + "/common/conf/master.cf":        true,
		base + "/common/conf":                  true,
		base + "/common/conf/../../conf/x":     false,
		base + "/common/conf/../sbin/postconf": false,
		base + "/conf/main.cf":                 false,
		base + "/common/confx/master.cf":       false,
		"conf/nginx.conf":                      false,
		"/etc/postfix/main.cf":                 false,
	} {
		if got := c.IsConfPath(path); got != want {
			t.Errorf("IsConfPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// A stale entry that cannot be removed must not hide the real error, and a
// staging directory that cannot be read is skipped.
func TestClearStagingFailuresAreNonFatal(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.UserID = 0
	f.FS.PutDir(staging, 0o700)
	f.FS.Put(staging+"/stale", []byte("x"), 0o600)
	f.FS.Fail = func(op, p string) error {
		if op == "RemoveAll" && strings.HasSuffix(p, "/stale") {
			return errors.New("busy")
		}

		return nil
	}

	if err := NewClient(base, f.System(), nil).Apply(context.Background(),
		postfixconf.Change{Unset: []string{"relayhost"}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	f.FS.Fail = func(op, _ string) error {
		if op == "ReadDir" {
			return errors.New("unreadable")
		}

		return nil
	}

	if err := NewClient(base, f.System(), nil).Apply(context.Background(),
		postfixconf.Change{Unset: []string{"relayhost"}}); err != nil {
		t.Fatalf("Apply with an unreadable staging dir: %v", err)
	}
}

func TestStagingDirCreationFailure(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.UserID = 0
	f.FS.Fail = func(op, _ string) error {
		if op == "MkdirAll" {
			return errors.New("read-only")
		}

		return nil
	}

	err := NewClient(base, f.System(), nil).Apply(context.Background(), postfixconf.Change{Unset: []string{"relayhost"}})
	if err == nil || !strings.Contains(err.Error(), "staging dir") {
		t.Fatalf("err = %v, want a staging-dir error", err)
	}

	if len(f.Exec.Lines()) != 0 {
		t.Error("the helper must not run when staging failed")
	}
}
