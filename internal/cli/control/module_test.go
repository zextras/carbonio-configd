// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const authReply = `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">` +
	`<soap:Body><AuthResponse xmlns="urn:zimbraAdmin" lifetime="43200000"><authToken>tok</authToken>` +
	`</AuthResponse></soap:Body></soap:Envelope>`

// moduleRun is the outcome of one `configd control` run of a module action.
type moduleRun struct {
	code           int
	stdout, stderr string
	queries        []string // the query string of every action request
}

// runModule runs opts against an Advanced endpoint answering reply with the
// given status. advanced says whether the Advanced jars are installed.
func runModule(t *testing.T, opts *Options, advanced bool, status int, reply string) moduleRun {
	t.Helper()

	var res moduleRun

	mux := http.NewServeMux()
	mux.HandleFunc("/service/admin/soap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(authReply))
	})
	mux.HandleFunc("/service/extension/zextras", func(w http.ResponseWriter, r *http.Request) {
		res.queries = append(res.queries, r.URL.RawQuery)

		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	o, f := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	o.newClient = func() (*zxadmin.Client, error) {
		return zxadmin.NewWithBaseURL(srv.URL, map[string]string{"zimbra_ldap_password": "secret"}), nil
	}

	if advanced {
		f.FS.PutDir(testBase+"/lib/ext/carbonio", 0o750)
		f.FS.Put(testBase+"/lib/ext/carbonio/carbonio-advanced-core-26.6.5.jar", nil, 0o600)
	}

	var stdout, stderr bytes.Buffer

	res.code = o.run(context.Background(), &stdout, &stderr, opts)
	res.stdout, res.stderr = stdout.String(), stderr.String()

	return res
}

const okReply = `{"response":{"message":"service stopped"},"ok":true}`

func TestModule_Success(t *testing.T) {
	tests := []struct {
		name  string
		opts  Options
		line  string
		query string
	}{
		{"stop module", Options{Action: ActionStop, Module: "backup"},
			"\tStopping backup module...Done.", "action=doStopService&module=ZxBackup&service_name=module"},
		{"start service", Options{Action: ActionStart, Module: "mobile", Service: "sync.v2"},
			"\tStarting mobile sync.v2 service...Done.", "action=doStartService&module=ZxMobile&service_name=sync.v2"},
		{"restart mailreplica", Options{Action: ActionRestart, Module: "mailreplica"},
			"\tRestarting mailreplica module...Done.", "action=doRestartService&module=ZxHA&service_name=module"},
		{"explicit module service", Options{Action: ActionStop, Module: "auth", Service: "module"},
			"\tStopping auth module...Done.", "action=doStopService&module=ZxAuth&service_name=module"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runModule(t, &tt.opts, true, http.StatusOK, okReply)

			if res.code != 0 || res.stderr != "" {
				t.Fatalf("code = %d, stderr = %q", res.code, res.stderr)
			}

			lines := strings.Split(strings.TrimRight(res.stdout, "\n"), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "Host ") || lines[1] != tt.line {
				t.Errorf("stdout = %q, want a Host line then %q", res.stdout, tt.line)
			}

			if len(res.queries) != 1 || res.queries[0] != tt.query {
				t.Errorf("queries = %v, want [%s]", res.queries, tt.query)
			}
		})
	}
}

func TestModule_RejectedBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		advance bool
		want    string
	}{
		{"unknown module", Options{Action: ActionStart, Module: "bogus"}, true,
			`Error: unknown module "bogus" (valid modules: addressbook, admin, auth, backup, config, mailreplica, mobile, powerstore)`},
		{"subscription", Options{Action: ActionStart, Module: "subscription"}, true, "Error: unknown module"},
		{"core", Options{Action: ActionStop, Module: "core"}, true,
			"Error: the core module and its services cannot be controlled separately: restart the mailbox instead (zmmailboxdctl restart)"},
		{"core restart", Options{Action: ActionRestart, Module: "core"}, true, "restart the mailbox instead"},
		{"core service", Options{Action: ActionStop, Module: "core", Service: "x"}, true, "restart the mailbox instead"},
		{"invalid service", Options{Action: ActionStart, Module: "backup", Service: "a b"}, true,
			`Error: invalid service name "a b"`},
		{"service leading dash", Options{Action: ActionStart, Module: "backup", Service: "-x"}, true, "invalid service name"},
		{"service with slash", Options{Action: ActionStart, Module: "backup", Service: "a/b"}, true, "invalid service name"},
		{"advanced not installed", Options{Action: ActionStart, Module: "backup"}, false,
			"Error: Carbonio Advanced is not installed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runModule(t, &tt.opts, tt.advance, http.StatusOK, okReply)

			if res.code != 1 {
				t.Errorf("code = %d, want 1", res.code)
			}

			if !strings.Contains(res.stderr, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", res.stderr, tt.want)
			}

			if res.stdout != "" || len(res.queries) != 0 {
				t.Errorf("stdout = %q, requests = %v, want none", res.stdout, res.queries)
			}
		})
	}
}

func TestModule_CoreHintOnSystemd(t *testing.T) {
	f := systest.New()
	f.FS.PutDir("/run/systemd/system", 0o755) // sd_booted(3)

	ctl := services.NewController(f.System(), services.WithBaseDir(testBase),
		services.WithConfigLoader(func() (map[string]string, error) { return map[string]string{}, nil }))
	o := &orchestrator{ctl: ctl}

	var stdout, stderr bytes.Buffer

	if code := o.run(context.Background(), &stdout, &stderr, &Options{Action: ActionStop, Module: "core"}); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}

	if want := "(systemctl restart carbonio-appserver.service)"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestModule_ServerRefusal(t *testing.T) {
	reply := `{"ok":false,"error":{"code":"SERVICE_CONDITIONS_NOT_MET",` +
		`"message":"Service conditions not met: {reason}","details":{"reason":"too many accounts"},"stackTrace":["x.Y"]}}`

	res := runModule(t, &Options{Action: ActionStart, Module: "mobile"}, true, http.StatusOK, reply)

	if res.code != 1 {
		t.Errorf("code = %d, want 1", res.code)
	}

	if !strings.HasSuffix(res.stdout, "\tStarting mobile module...Failed.\n") {
		t.Errorf("stdout = %q", res.stdout)
	}

	if want := "Error: Service conditions not met: too many accounts\n"; res.stderr != want {
		t.Errorf("stderr = %q, want %q", res.stderr, want)
	}
}

func TestModule_AdvancedNotRunning(t *testing.T) {
	res := runModule(t, &Options{Action: ActionRestart, Module: "backup"}, true, http.StatusNotFound, "<html>404</html>")

	if res.code != 1 {
		t.Errorf("code = %d, want 1", res.code)
	}

	if !strings.HasSuffix(res.stdout, "\tRestarting backup module...Failed.\n") {
		t.Errorf("stdout = %q", res.stdout)
	}

	if want := "Error: Carbonio Advanced is not running\n"; res.stderr != want {
		t.Errorf("stderr = %q, want %q", res.stderr, want)
	}
}

func TestModule_MailboxUnreachable(t *testing.T) {
	o, f := testOrchestrator(map[string]*services.ServiceDef{}, map[string]string{})
	f.FS.PutDir(testBase+"/lib/ext/carbonio", 0o750)
	f.FS.Put(testBase+"/lib/ext/carbonio/carbonio-advanced-core-26.6.5.jar", nil, 0o600)

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more

	o.newClient = func() (*zxadmin.Client, error) {
		return zxadmin.NewWithBaseURL(url, map[string]string{"zimbra_ldap_password": "secret"}), nil
	}

	var stdout, stderr bytes.Buffer

	if code := o.run(context.Background(), &stdout, &stderr, &Options{Action: ActionStop, Module: "backup"}); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}

	if !strings.HasPrefix(stderr.String(), "Error: ") || strings.Contains(stderr.String(), "not running") {
		t.Errorf("stderr = %q, want a connection error", stderr.String())
	}
}

func TestModules_Sorted(t *testing.T) {
	got := strings.Join(Modules(), " ")
	if want := "addressbook admin auth backup config mailreplica mobile powerstore"; got != want {
		t.Errorf("Modules() = %q, want %q", got, want)
	}
}
