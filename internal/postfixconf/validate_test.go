// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfixconf

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAssignment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, key, value string
		wantErr          bool
	}{
		{"plain", "myhostname", "mail.example.com", false},
		{"mixed case key", "lmtp_tls_CAfile", "/ca", false},
		{"empty value", "relayhost", "", false},
		{"owner ok", "mail_owner", "postfix", false},
		{"group ok", "setgid_group", "postdrop", false},
		{"owner root", "mail_owner", "root", true},
		{"owner other account", "mail_owner", "zextras", true},
		{"group other", "setgid_group", "docker", true},
		{"owner empty", "mail_owner", "", true},
		{"empty key", "", "x", true},
		{"dash key", "-X", "x", true},
		{"qualified key", "smtp/inet/foo", "x", true},
		{"equals in key", "a=b", "x", true},
		{"unknown key", "some_unknown_key", "x", true},
		{"upper-case variant", "MyHostname", "x", true},
		{"LF in value", "myhostname", "a\nb", true},
		{"CR in value", "myhostname", "a\rb", true},
		{"NUL in value", "myhostname", "a\x00b", true},
		{"value at cap", "myhostname", strings.Repeat("a", MaxValueSize), false},
		{"value over cap", "myhostname", strings.Repeat("a", MaxValueSize+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateAssignment(tc.key, tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateAssignment(%q, ...) = %v, wantErr %v", tc.key, err, tc.wantErr)
			}

			if err != nil && !errors.Is(err, ErrValidation) {
				t.Errorf("error %v does not wrap ErrValidation", err)
			}
		})
	}
}

// Keys that make the root Postfix exec or open attacker-chosen paths must
// never be on the allowlist.
func TestAllowedKeysExcludeDangerousKeys(t *testing.T) {
	t.Parallel()

	for _, k := range []string{
		"debugger_command", "tls_config_file", "tls_config_name", "default_privs",
		"virtual_minimum_uid", "virtual_uid_maps", "virtual_gid_maps",
		"config_directory", "daemon_directory", "command_directory", "meta_directory",
		"shlib_directory", "queue_directory", "data_directory", "sample_directory",
		"readme_directory", "html_directory", "manpage_directory", "sendmail_path",
		"newaliases_path", "mailq_path", "import_environment", "export_environment",
		"alternate_config_directories", "multi_instance_directories",
		"multi_instance_wrapper", "mail_spool_directory", "home_mailbox",
		"process_id_directory", "virtual_mailbox_base",
	} {
		if IsAllowedKey(k) {
			t.Errorf("%s must not be allowlisted", k)
		}

		if err := ValidateKey(k); !errors.Is(err, ErrValidation) {
			t.Errorf("ValidateKey(%s) = %v, want ErrValidation", k, err)
		}
	}

	for k := range allowedKeys {
		if !keyRe.MatchString(k) {
			t.Errorf("allowlisted key %q is not a valid name", k)
		}
	}
}

const goodMaster = `# comment
smtp      inet  n       -       n       -       -       smtpd
pickup    unix  n       -       n       60      1       pickup
  -o content_filter=
# comment between continuation lines
  -o receive_override_options=no_header_body_checks
trivial-rewrite unix -  -       n       -       -       trivial-rewrite
smtp-amavis unix -      -       n       -       2       smtp
  -o smtp_data_done_timeout=1200
tlsmgr    unix  -       -       n       1000?   1       tlsmgr
[127.0.0.1]:10025 inet n  -       n       -       -       smtpd
  -o { mynetworks = 127.0.0.0/8 }
  -o{ smtpd_banner = x }
465       inet  n       -       n       -       -       smtpd
	-o smtpd_tls_wrappermode=yes
	-o smtpd_client_restrictions=
policy    unix  -       n       n       -       0       spawn
	user=zimbra argv=/usr/bin/perl /opt/zextras/libexec/zmpostfixpolicyd
`

const goodEntry = "smtp inet n - n - - smtpd"

func TestValidateMasterCf(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		wantErr string // substring; empty means valid
	}{
		{"good file", goodMaster, ""},
		{"empty", "", ""},
		{"too few fields", "smtp inet n - n - smtpd\n", "need at least 8"},
		{"command with slash", "smtp inet n - n - - /tmp/evil\n", "not a Postfix daemon"},
		{"command absolute", "smtp inet n - n - - /opt/zextras/bin/sh\n", "not a Postfix daemon"},
		{"command traversal", "smtp inet n - n - - ../../tmp/x\n", "not a Postfix daemon"},
		{"command uppercase", "smtp inet n - n - - Smtpd\n", "not a Postfix daemon"},
		{"command post-install", "smtp inet n - n - - post-install\n", "not a Postfix daemon"},
		{"command postfix-script", "smtp inet n - n - - postfix-script\n", "not a Postfix daemon"},
		{"command master", "smtp inet n - n - - master\n", "not a Postfix daemon"},
		{"service name with slash", "../x unix - n n - - smtpd\n", "invalid service name"},
		{"service name with dotdot", "a..b unix - n n - - smtpd\n", "invalid service name"},
		{"service name with slash 2", "a/b unix - n n - - smtpd\n", "invalid service name"},
		{"bad type", "smtp tcp n - n - - smtpd\n", "invalid type"},
		{"bad private", "smtp inet x - n - - smtpd\n", "must be y, n or -"},
		{"bad chroot", "smtp inet n - maybe - - smtpd\n", "must be y, n or -"},
		{"bad wakeup", "smtp inet n - n 1x - smtpd\n", "wakeup"},
		{"bad maxproc", "smtp inet n - n - 1? smtpd\n", "maxproc"},
		{"spawn as root", "evil unix - - n - - spawn user=root argv=/bin/sh\n", "as root"},
		{"spawn as uid 0", "evil unix - - n - - spawn user=0:0 argv=/bin/sh\n", "as root"},
		{"pipe macros", "m unix - n n - - pipe\n  flags=DRhu user=vmail argv=/x/maildrop -d ${recipient} ${extension}\n", ""},
		{"macro braces outside pipe", goodEntry + " ${recipient}\n", "outside a -o"},
		{"pipe stray brace", "m unix - n n - - pipe user=vmail argv=/x {recipient\n", "outside a -o"},
		{"pipe -o override still checked", "m unix - n n - - pipe user=vmail argv=/x -o daemon_directory=/t\n", "not allowed"},
		{"spawn as user", "ok unix - - n - - spawn user=zextras argv=/bin/true\n", ""},
		{"deny key via -o", goodEntry + " -o daemon_directory=/tmp\n", "not allowed"},
		{"deny key attached", goodEntry + " -odaemon_directory=/tmp\n", "not allowed"},
		{"deny key in cluster", goodEntry + " -vodaemon_directory=/tmp\n", "option"},
		{"deny key in cluster split", goodEntry + " -vo daemon_directory=/tmp\n", "option"},
		{"deny key on continuation", goodEntry + "\n  -o import_environment=LD_PRELOAD=/x\n", "not allowed"},
		{"deny key on tab continuation", goodEntry + "\n\t-o sendmail_path=/x\n", "not allowed"},
		{"deny key in braces", goodEntry + "\n  -o { daemon_directory = /tmp }\n", "not allowed"},
		{"deny key brace-wrapped -o", goodEntry + " { -o daemon_directory=/tmp }\n", "not allowed"},
		{"deny key brace glued", goodEntry + " {-o daemon_directory=/tmp}\n", "not allowed"},
		{"deny key brace attached", goodEntry + " -o{daemon_directory=/tmp}\n", "not allowed"},
		{"debugger_command override", goodEntry + " -o debugger_command=/tmp/x\n", "not allowed"},
		{"tls_config_file override", goodEntry + " -o tls_config_file=/tmp/x\n", "not allowed"},
		{"tls_config_name override", goodEntry + " -o tls_config_name=x\n", "not allowed"},
		{"default_privs override", goodEntry + " -o default_privs=root\n", "not allowed"},
		{"virtual_uid_maps override", goodEntry + " -o virtual_uid_maps=static:0\n", "not allowed"},
		{"virtual_minimum_uid override", goodEntry + " -o virtual_minimum_uid=0\n", "not allowed"},
		{"mail_owner override", goodEntry + " -o mail_owner=root\n", "not allowed"},
		{"wrapped, closing brace apart", goodEntry + " { -o smtpd_banner=x }\n", ""},
		{"wrapped, closing brace glued", goodEntry + " { -o smtpd_banner=x}\n", ""},
		{"wrapped, glued opening", goodEntry + " {-o smtpd_banner=x }\n", ""},
		{"wrapped, separate -o argument", goodEntry + " { -o smtpd_banner=x }\n", ""},
		{"wrapped, never closed", goodEntry + " { -o smtpd_banner=x\n", "unterminated"},
		{"wrapped with a nested group", goodEntry + " { -o { smtpd_banner = x } }\n", "nested"},
		{"brace group without -o", goodEntry + " {-x}\n", "outside a -o"},
		{"-D", goodEntry + " -D\n", "not allowed"},
		{"-vD", goodEntry + " -vD\n", "not allowed"},
		{"-D on continuation", goodEntry + "\n  -D\n", "not allowed"},
		{"-o without value", goodEntry + " -o\n", "without an argument"},
		{"-o name only", goodEntry + " -o foo\n", "NAME=VALUE"},
		{"-o bad name", goodEntry + " -o a/b/c=1\n", "invalid -o parameter name"},
		{"unterminated brace", goodEntry + " -o { a = b\n", "unterminated"},
		{"stray open brace", goodEntry + " {\n", "outside a -o"},
		{"stray close brace", goodEntry + " }\n", "outside a -o"},
		{"brace in plain arg", goodEntry + " foo{bar}\n", "outside a -o"},
		{"brace after value", goodEntry + " -o a=b}\n", "inside a -o value"},
		{"CR byte", goodEntry + "\r\n", "outside printable ASCII"},
		{"NUL byte", goodEntry + "\x00\n", "outside printable ASCII"},
		{"U+00A0 separator", "a\u00a0b unix - n n - - ../../../../tmp/evil\n", "outside printable ASCII"},
		{"U+0085", "a\u0085b unix - n n - - ../x\n", "outside printable ASCII"},
		{"U+3000", "a\u3000b unix - n n - - ../x\n", "outside printable ASCII"},
		{"vertical tab", "a\vb unix - n n - - ../x\n", "outside printable ASCII"},
		{"form feed", "a\fb unix - n n - - ../x\n", "outside printable ASCII"},
		{"DEL", goodEntry + "\x7f\n", "outside printable ASCII"},
		{"blank then indented", goodEntry + "\n\n evil unix - n n - - ../../../tmp/evil\n", "does not continue"},
		{"whitespace-only then indented", goodEntry + "\n  \n evil unix - n n - - ../x\n", "does not continue"},
		{"first line indented", " evil unix - n n - - ../x\n" + goodEntry + "\n", "does not continue"},
		{"indented comment", goodEntry + "\n  # x\n  evil unix - n n - - ../x\n", "indented comment"},
		{"comment with -o ignored", "# -o daemon_directory=/tmp\n" + goodEntry + "\n", ""},
		{"blank trailing whitespace", goodEntry + "\n   \n", ""},
		{"oversize", strings.Repeat("# x\n", MaxFileSize), "larger than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateFile(MasterCf, []byte(tc.content))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q", tc.wantErr)
			case tc.wantErr != "":
				if !strings.Contains(err.Error(), tc.wantErr) || !errors.Is(err, ErrValidation) {
					t.Fatalf("error %q, want substring %q wrapping ErrValidation", err, tc.wantErr)
				}
			}
		})
	}
}

func TestValidationErrorsDoNotEchoContent(t *testing.T) {
	t.Parallel()

	secret := "SECRET-ROOT-DATA"

	for _, err := range []error{
		ValidateKey(secret + "/x"),
		ValidateMasterCf([]byte("a/" + secret + " unix - n n - - smtpd\n")),
		ValidateMasterCf([]byte(goodEntry + " -o " + secret + "/x=1\n")),
		ValidateMasterCf([]byte(goodEntry + " -" + secret + "o\n")),
		ValidateMasterCf([]byte(goodEntry + " foo=" + secret + " {\n")),
	} {
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("error %v must exist and not echo staged content", err)
		}
	}
}

func TestValidateFile(t *testing.T) {
	t.Parallel()

	for _, name := range []string{TagAsForeignRe, TagAsOriginatingRe} {
		if err := ValidateFile(name, []byte("/x/ -o daemon_directory=1\n\r\n")); err != nil {
			t.Errorf("%s is free-form data: %v", name, err)
		}

		if err := ValidateFile(name, make([]byte, MaxFileSize+1)); err == nil {
			t.Errorf("%s over the size cap must be rejected", name)
		}
	}

	if err := ValidateFile("main.cf", nil); err == nil {
		t.Error("main.cf must not be an installable file")
	}

	if err := ValidateFile("../master.cf", nil); err == nil {
		t.Error("a path must not be an installable file")
	}
}

func TestChangeValidate(t *testing.T) {
	t.Parallel()

	good := Change{
		Files: map[string][]byte{MasterCf: []byte(goodMaster)},
		Unset: []string{"smtpd_banner"},
		Set:   []KV{{"myhostname", "h"}},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good change: %v", err)
	}

	for name, c := range map[string]Change{
		"bad unset": {Unset: []string{"daemon_directory"}},
		"bad set":   {Set: []KV{{"myhostname", "h"}, {"queue_directory", "/x"}}},
		"bad owner": {Set: []KV{{"mail_owner", "zextras"}}},
		"bad file":  {Files: map[string][]byte{"evil": nil}},
	} {
		if err := c.Validate(); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}
