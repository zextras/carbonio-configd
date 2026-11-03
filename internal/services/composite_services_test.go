// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// --- aliases, canonical names, ordering ---

func TestServiceAliases_Table(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"mailboxd":       "mailbox",
		"mysql":          "appserver-db",
		"mariadb":        "appserver-db",
		"service":        "store",
		"zmconfigd":      "configd",
		"directory":      "ldap",
		"config-service": "configd",
	}

	for alias, canonical := range want {
		if got := ServiceAliases[alias]; got != canonical {
			t.Errorf("ServiceAliases[%q] = %q, want %q", alias, got, canonical)
		}
	}

	if _, ok := ServiceAliases["clamd"]; ok {
		t.Error("clamd must not be an alias: it is its own service")
	}
}

func TestLookupService_AliasesResolveToCanonical(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	for alias, canonical := range map[string]string{
		"mailboxd":       "mailbox",
		"mysql":          "appserver-db",
		"mariadb":        "appserver-db",
		"service":        "store",
		"zmconfigd":      "configd",
		"directory":      "ldap",
		"config-service": "configd",
	} {
		def := ctl.LookupService(alias)
		if def == nil {
			t.Errorf("LookupService(%q) = nil", alias)

			continue
		}

		if def.Name != canonical {
			t.Errorf("LookupService(%q).Name = %q, want %q", alias, def.Name, canonical)
		}
	}
}

func TestLookupService_ClamdIsOwnService(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	def := ctl.LookupService("clamd")
	if def == nil || def.Name != "clamd" {
		t.Fatalf("LookupService(clamd) = %+v, want the clamd service", def)
	}

	if len(def.Members) != 0 {
		t.Errorf("clamd must not be a composite, members = %v", def.Members)
	}

	if !def.Component {
		t.Error("clamd should be a component of antivirus")
	}

	if ctl.LookupService("antivirus") == nil || ctl.LookupService("antivirus").Name != "antivirus" {
		t.Error("antivirus must resolve to itself")
	}

	if ctl.LookupService("no-such-service") != nil {
		t.Error("unknown name must resolve to nil")
	}
}

func TestMapLDAPServiceToRegistry_Composite(t *testing.T) {
	t.Parallel()

	for ldapName, want := range map[string]string{
		"mailbox":          "store",
		"service":          "store",
		"zmconfigd":        "configd",
		"directory-server": "ldap",
		"antispam":         "antispam",
		"antivirus":        "antivirus",
		"proxy":            "proxy",
	} {
		if got := MapLDAPServiceToRegistry(ldapName); got != want {
			t.Errorf("MapLDAPServiceToRegistry(%q) = %q, want %q", ldapName, got, want)
		}
	}
}

func TestControlServiceNames_ExcludesComponents(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	control := ctl.ControlServiceNames()
	all := ctl.AllServiceNames()

	for _, component := range []string{"appserver-db", "mailbox", "amavis", "clamd", "antispam-db"} {
		if slices.Contains(control, component) {
			t.Errorf("ControlServiceNames contains component %q", component)
		}

		if !slices.Contains(all, component) {
			t.Errorf("AllServiceNames misses component %q", component)
		}
	}

	for _, composite := range []string{"store", "antivirus", "antispam"} {
		if !slices.Contains(control, composite) {
			t.Errorf("ControlServiceNames misses composite %q", composite)
		}
	}

	for _, name := range control {
		if ctl.registry[name].Component {
			t.Errorf("ControlServiceNames contains component %q", name)
		}
	}

	for _, alias := range []string{"mailboxd", "mysql", "mariadb", "service"} {
		if slices.Contains(all, alias) {
			t.Errorf("AllServiceNames lists alias %q", alias)
		}
	}
}

func TestDefaultStartOrder_NewServices(t *testing.T) {
	t.Parallel()

	order := getDefaultStartOrder()

	for name, want := range map[string]int{
		"appserver-db": 48, "store": 49, "mailbox": 50, "antispam-db": 79, "clamd": 91,
	} {
		if order[name] != want {
			t.Errorf("start order of %s = %d, want %d", name, order[name], want)
		}
	}

	ctl, _ := newFakeController(t)
	all := ctl.AllServiceNames()
	idx := func(n string) int { return slices.Index(all, n) }

	chain := []string{
		"ldap", "configd", "appserver-db", "store", "mailbox", "memcached", "proxy",
		"amavis", "antispam-db", "antispam", "antivirus", "clamd", "freshclam", "opendkim", "mta", "stats",
	}

	for i := range chain {
		if idx(chain[i]) < 0 {
			t.Fatalf("%s missing from AllServiceNames %v", chain[i], all)
		}

		if i > 0 && idx(chain[i-1]) >= idx(chain[i]) {
			t.Errorf("%s must sort before %s in %v", chain[i-1], chain[i], all)
		}
	}
}

func TestDefaultRegistry_CompositeDefinitions(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	store := ctl.LookupService("store")
	if !slices.Equal(store.Members, []string{"appserver-db", "mailbox"}) {
		t.Errorf("store members = %v", store.Members)
	}

	if store.StopMembersForward || !store.ForceMembersNoRewrite {
		t.Errorf("store: StopMembersForward=%v ForceMembersNoRewrite=%v, want false/true",
			store.StopMembersForward, store.ForceMembersNoRewrite)
	}

	av := ctl.LookupService("antivirus")
	if !slices.Equal(av.Members, []string{"amavis", "clamd", "freshclam"}) {
		t.Errorf("antivirus members = %v", av.Members)
	}

	if !av.StopMembersForward || !av.ForceMembersNoRewrite || av.DetailMember != "clamd" {
		t.Errorf("antivirus flags wrong: %+v", av)
	}

	as := ctl.LookupService("antispam")
	if !slices.Equal(as.Members, []string{"antispam-db", "amavis"}) {
		t.Errorf("antispam members = %v", as.Members)
	}

	if !as.StopMembersForward || !as.IdleWhenDisabled || as.EnableCheck == nil {
		t.Errorf("antispam flags wrong: %+v", as)
	}

	if got := ctl.LookupService("mailbox").Wants; !slices.Equal(got, []string{"appserver-db"}) {
		t.Errorf("mailbox Wants = %v", got)
	}
}

// --- composite lifecycle over a recording registry ---

// recorder is a concurrency-safe event log shared by hooks.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.events)
}

// memberDef is a leaf service whose start/stop are recorded; startErr/stopErr
// make them fail. rewrite records whether the start ran with rewrite skipped.
func memberDef(ctl **Controller, rec *recorder, name string, startErr, stopErr error) *ServiceDef {
	return &ServiceDef{
		Name:          name,
		DisplayName:   name,
		ConfigRewrite: []string{name + "-conf"},
		CustomStart: func(ctx context.Context, _ *ServiceDef) error {
			rec.add("start:" + name + ":skip=" + boolStr((*ctl).rewriteSkipped(ctx)))

			return startErr
		},
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			rec.add("stop:" + name)

			return stopErr
		},
	}
}

// newRewritableController is a fake Controller whose configrewrite script
// exists, so config rewrites never fall back to the network.
func newRewritableController(t *testing.T, reg map[string]*ServiceDef) *Controller {
	t.Helper()

	ctl, f := newFakeController(t, WithRegistry(reg))
	f.FS.Put(ctl.p.configrewrite, []byte("#!/bin/sh\n"), 0o755)

	return ctl
}

func boolStr(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

// compositeFixture builds a Controller over a registry of recording members
// a, b, c and the composite def given by shape.
func compositeFixture(t *testing.T, shape ServiceDef, startErrs, stopErrs map[string]error) (*Controller, *recorder, func() []string) {
	t.Helper()

	rec := &recorder{}

	var ctl *Controller

	reg := map[string]*ServiceDef{}

	for _, m := range []string{"a", "b", "c"} {
		reg[m] = memberDef(&ctl, rec, m, startErrs[m], stopErrs[m])
	}

	shape.Name = "comp"
	shape.DisplayName = "comp"
	shape.Members = []string{"a", "b", "c"}
	reg["comp"] = &shape

	ctl, f := newFakeController(t, WithRegistry(reg))
	f.FS.Put(ctl.p.configrewrite, []byte("#!/bin/sh\n"), 0o755)

	return ctl, rec, func() []string {
		var out []string

		for _, l := range f.Exec.Lines() {
			if strings.HasPrefix(l, ctl.p.configrewrite) {
				out = append(out, l)
			}
		}

		return out
	}
}

func TestComposite_StartInMemberOrder_StopReverse(t *testing.T) {
	t.Parallel()

	ctl, rec, _ := compositeFixture(t, ServiceDef{}, nil, nil)

	if err := ctl.ServiceStart(context.Background(), "comp"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := ctl.ServiceStop(context.Background(), "comp"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	want := []string{
		"start:a:skip=false", "start:b:skip=false", "start:c:skip=false",
		"stop:c", "stop:b", "stop:a",
	}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestComposite_StopMembersForward(t *testing.T) {
	t.Parallel()

	ctl, rec, _ := compositeFixture(t, ServiceDef{StopMembersForward: true}, nil, nil)

	if err := ctl.ServiceStop(context.Background(), "comp"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if got, want := rec.list(), []string{"stop:a", "stop:b", "stop:c"}; !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestComposite_ForceMembersNoRewrite_CompositeRewritesOnce(t *testing.T) {
	t.Parallel()

	ctl, rec, rewrites := compositeFixture(t,
		ServiceDef{ForceMembersNoRewrite: true, ConfigRewrite: []string{"s1", "s2"}}, nil, nil)

	if err := ctl.ServiceStart(context.Background(), "comp"); err != nil {
		t.Fatalf("start: %v", err)
	}

	want := []string{"start:a:skip=true", "start:b:skip=true", "start:c:skip=true"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}

	got := rewrites()
	if len(got) != 1 || got[0] != ctl.p.configrewrite+" s1 s2" {
		t.Errorf("configrewrite calls = %v, want exactly one for the composite sections", got)
	}
}

func TestComposite_WithoutForceNoRewrite_MembersRewriteThemselves(t *testing.T) {
	t.Parallel()

	ctl, rec, rewrites := compositeFixture(t, ServiceDef{}, nil, nil)

	if err := ctl.ServiceStart(context.Background(), "comp"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if got, want := rec.list(), []string{"start:a:skip=false", "start:b:skip=false", "start:c:skip=false"}; !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}

	if got := len(rewrites()); got != 3 {
		t.Errorf("configrewrite calls = %v, want one per member", rewrites())
	}
}

func TestComposite_NoRewriteContext_SkipsCompositeRewrite(t *testing.T) {
	t.Parallel()

	ctl, _, rewrites := compositeFixture(t,
		ServiceDef{ForceMembersNoRewrite: true, ConfigRewrite: []string{"s1"}}, nil, nil)

	if err := ctl.ServiceStart(WithNoRewriteContext(context.Background()), "comp"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if got := rewrites(); len(got) != 0 {
		t.Errorf("configrewrite calls = %v, want none", got)
	}
}

func TestComposite_StopFailureContinuesAndJoinsErrors(t *testing.T) {
	t.Parallel()

	errC, errA := errors.New("boom-c"), errors.New("boom-a")
	ctl, rec, _ := compositeFixture(t, ServiceDef{}, nil, map[string]error{"c": errC, "a": errA})

	err := ctl.ServiceStop(context.Background(), "comp")
	if err == nil {
		t.Fatal("expected a joined error")
	}

	if !errors.Is(err, errC) || !errors.Is(err, errA) {
		t.Errorf("error %v does not wrap both member failures", err)
	}

	if !strings.Contains(err.Error(), "c:") || !strings.Contains(err.Error(), "a:") {
		t.Errorf("error %q should name failing members", err)
	}

	if got, want := rec.list(), []string{"stop:c", "stop:b", "stop:a"}; !slices.Equal(got, want) {
		t.Errorf("every member must be stopped despite failures: %v, want %v", got, want)
	}
}

func TestComposite_StartFailureContinuesAndJoinsErrors(t *testing.T) {
	t.Parallel()

	errA := errors.New("boom-a")
	ctl, rec, _ := compositeFixture(t, ServiceDef{}, map[string]error{"a": errA}, nil)

	err := ctl.ServiceStart(context.Background(), "comp")
	if !errors.Is(err, errA) {
		t.Fatalf("err = %v, want it to wrap %v", err, errA)
	}

	if got := rec.list(); len(got) != 3 {
		t.Errorf("all members must be attempted, events = %v", got)
	}
}

func TestComposite_MemberAppliesFiltersLifecycle(t *testing.T) {
	t.Parallel()

	ctl, rec, _ := compositeFixture(t, ServiceDef{
		MemberApplies: func(_ context.Context, m string) bool { return m != "b" },
	}, nil, nil)

	if err := ctl.ServiceStart(context.Background(), "comp"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := ctl.ServiceStop(context.Background(), "comp"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	want := []string{"start:a:skip=false", "start:c:skip=false", "stop:c", "stop:a"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestStore_AliasServiceStartsCompositeInOrder(t *testing.T) {
	t.Parallel()

	rec := &recorder{}

	var ctl *Controller

	reg := map[string]*ServiceDef{
		"appserver-db": memberDef(&ctl, rec, "appserver-db", nil, nil),
		"mailbox":      memberDef(&ctl, rec, "mailbox", nil, nil),
		"store": {
			Name: "store", Members: []string{"appserver-db", "mailbox"}, ForceMembersNoRewrite: true,
		},
	}

	ctl = newRewritableController(t, reg)

	if err := ctl.ServiceStart(context.Background(), "service"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := ctl.ServiceStop(context.Background(), "service"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	want := []string{
		"start:appserver-db:skip=true", "start:mailbox:skip=true",
		"stop:mailbox", "stop:appserver-db",
	}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestAntivirusAntispam_StopForwardOverRealDefinitions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		members []string
	}{
		{"antivirus", []string{"amavis", "clamd", "freshclam"}},
		{"antispam", []string{"antispam-db", "amavis"}},
	} {
		ctl, _ := newFakeController(t)
		def := ctl.LookupService(tc.name)

		if got := def.stopOrder(tc.members); !slices.Equal(got, tc.members) {
			t.Errorf("%s stop order = %v, want forward %v", tc.name, got, tc.members)
		}
	}

	ctl, _ := newFakeController(t)
	store := ctl.LookupService("store")

	if got := store.stopOrder([]string{"appserver-db", "mailbox"}); !slices.Equal(got, []string{"mailbox", "appserver-db"}) {
		t.Errorf("store stop order = %v, want reverse", got)
	}
}

// --- Wants ---

func TestWants_FailureOnlyWarns(t *testing.T) {
	t.Parallel()

	rec := &recorder{}

	var ctl *Controller

	dbErr := errors.New("db down")
	reg := map[string]*ServiceDef{
		"appserver-db": memberDef(&ctl, rec, "appserver-db", dbErr, nil),
		"mailbox":      memberDef(&ctl, rec, "mailbox", nil, nil),
	}
	reg["mailbox"].Wants = []string{"appserver-db"}

	ctl = newRewritableController(t, reg)

	if err := ctl.ServiceStart(context.Background(), "mailbox"); err != nil {
		t.Fatalf("mailbox start must succeed when its Wants fail: %v", err)
	}

	want := []string{"start:appserver-db:skip=false", "start:mailbox:skip=false"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestWants_NotStoppedWithService(t *testing.T) {
	t.Parallel()

	rec := &recorder{}

	var ctl *Controller

	reg := map[string]*ServiceDef{
		"appserver-db": memberDef(&ctl, rec, "appserver-db", nil, nil),
		"mailbox":      memberDef(&ctl, rec, "mailbox", nil, nil),
	}
	reg["mailbox"].Wants = []string{"appserver-db"}

	ctl = newRewritableController(t, reg)

	if err := ctl.ServiceStop(context.Background(), "mailbox"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if got := rec.list(); !slices.Equal(got, []string{"stop:mailbox"}) {
		t.Errorf("events = %v, want only the mailbox stop", got)
	}
}

// --- composite status over the default registry and the fake process table ---

const fakeMariaDBArg = "--defaults-file="

func (c *Controller) addProc(f interface{ Add(sys.ProcEntry) }, pid int, exe string, args ...string) {
	f.Add(sys.ProcEntry{PID: pid, Exe: exe, Argv: append([]string{exe}, args...)})
}

func TestStoreStatus_RunningOnlyWhenBothMembersRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctx := context.Background()

	assertStatus := func(want bool, why string) {
		t.Helper()

		got, err := ctl.ServiceStatus(ctx, "store")
		if err != nil {
			t.Fatalf("%s: %v", why, err)
		}

		if got != want {
			t.Errorf("%s: store running = %v, want %v", why, got, want)
		}
	}

	assertStatus(false, "nothing running")

	ctl.addProc(f.Proc, 500, ctl.p.common+"/sbin/mariadbd", fakeMariaDBArg+ctl.p.conf+"/my.cnf")
	assertStatus(false, "database only")

	ctl.addProc(f.Proc, 501, "/opt/zextras/common/lib/jvm/bin/java", mailboxMainClass)
	assertStatus(true, "database and JVM")

	f.Proc.Remove(500)
	assertStatus(false, "JVM only")

	// The mailbox component alone reports on the JVM, whatever the database does.
	if up, err := ctl.ServiceStatus(ctx, "mailbox"); err != nil || !up {
		t.Errorf("mailbox status = %v, %v; want running", up, err)
	}
}

func TestAntivirusStatus_AmavisNotInstalledIsSkipped(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctx := context.Background()

	if ctl.amavisMemberApplies(ctx, "amavis") {
		t.Fatal("amavis must not apply when amavisd is not installed")
	}

	if !ctl.amavisMemberApplies(ctx, "clamd") || !ctl.amavisMemberApplies(ctx, "freshclam") {
		t.Fatal("non-amavis members always apply")
	}

	ctl.addProc(f.Proc, 600, ctl.p.common+"/sbin/clamd")

	if up, _ := ctl.ServiceStatus(ctx, "antivirus"); up {
		t.Error("antivirus must not be running with freshclam down")
	}

	ctl.addProc(f.Proc, 601, ctl.p.common+"/bin/freshclam")

	up, err := ctl.ServiceStatus(ctx, "antivirus")
	if err != nil || !up {
		t.Errorf("antivirus (no amavis installed, clamd+freshclam up) = %v, %v; want running", up, err)
	}

	if got := ctl.RunningPID(ctx, ctl.LookupService("antivirus")); got != 600 {
		t.Errorf("antivirus detail pid = %d, want the clamd pid 600", got)
	}
}

func TestAntivirusStatus_AmavisInstalledIsRequired(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctx := context.Background()

	f.FS.Put(ctl.p.common+"/sbin/amavisd", []byte("x"), 0o755)

	if !ctl.amavisMemberApplies(ctx, "amavis") {
		t.Fatal("amavis must apply once amavisd is installed")
	}

	ctl.addProc(f.Proc, 600, ctl.p.common+"/sbin/clamd")
	ctl.addProc(f.Proc, 601, ctl.p.common+"/bin/freshclam")

	if up, _ := ctl.ServiceStatus(ctx, "antivirus"); up {
		t.Error("antivirus must not be running when installed amavis is down")
	}

	// clamd on its own is a distinct, running service.
	if up, err := ctl.ServiceStatus(ctx, "clamd"); err != nil || !up {
		t.Errorf("clamd status = %v, %v; want running", up, err)
	}
}

func TestAntispamMemberApplies(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctx := context.Background()

	if ctl.antispamMemberApplies(ctx, "antispam-db") {
		t.Error("antispam-db must not apply without antispam_mysql_enabled")
	}

	if ctl.antispamMemberApplies(ctx, "amavis") {
		t.Error("amavis must not apply when not installed")
	}

	f.FS.Put(ctl.p.common+"/sbin/amavisd", []byte("x"), 0o755)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{"antispam_mysql_enabled": "TRUE", "antispam_mysql_host": "localhost"}, nil
	}

	if !ctl.antispamMemberApplies(ctx, "antispam-db") || !ctl.antispamMemberApplies(ctx, "amavis") {
		t.Error("both members must apply with local database and amavisd installed")
	}
}

// --- idle antispam ---

func antispamFixture(t *testing.T, enabled bool) (*Controller, *recorder) {
	t.Helper()

	rec := &recorder{}

	var ctl *Controller

	reg := map[string]*ServiceDef{
		"antispam-db": memberDef(&ctl, rec, "antispam-db", nil, nil),
		"amavis":      memberDef(&ctl, rec, "amavis", nil, nil),
		"antispam": {
			Name:               "antispam",
			Members:            []string{"antispam-db", "amavis"},
			StopMembersForward: true,
			EnableCheck:        func(context.Context) bool { return enabled },
			IdleWhenDisabled:   true,
		},
	}

	ctl = newRewritableController(t, reg)

	return ctl, rec
}

func TestAntispam_DisabledIsIdle(t *testing.T) {
	t.Parallel()

	ctl, rec := antispamFixture(t, false)
	ctx := context.Background()

	if !ctl.ServiceDisabled(ctx, "antispam") {
		t.Error("ServiceDisabled(antispam) = false, want true")
	}

	if err := ctl.ServiceStart(ctx, "antispam"); err != nil {
		t.Errorf("start must be a no-op: %v", err)
	}

	if err := ctl.ServiceStop(ctx, "antispam"); err != nil {
		t.Errorf("stop must be a no-op: %v", err)
	}

	if got := rec.list(); len(got) != 0 {
		t.Errorf("no member may be touched when disabled, events = %v", got)
	}
}

func TestAntispam_EnabledStartsMembers(t *testing.T) {
	t.Parallel()

	ctl, rec := antispamFixture(t, true)
	ctx := context.Background()

	if ctl.ServiceDisabled(ctx, "antispam") {
		t.Error("ServiceDisabled(antispam) = true, want false")
	}

	if err := ctl.ServiceStart(ctx, "antispam"); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := ctl.ServiceStop(ctx, "antispam"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	want := []string{
		"start:antispam-db:skip=false", "start:amavis:skip=false",
		"stop:antispam-db", "stop:amavis",
	}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestAntispam_DefaultRegistryIdleWithoutLDAPService(t *testing.T) {
	t.Parallel()

	// An empty localconfig cannot discover services: antispam counts as
	// disabled, so every verb is a successful no-op with no process touched.
	ctl, f := newFakeController(t)
	ctx := context.Background()

	if !ctl.ServiceDisabled(ctx, "antispam") {
		t.Fatal("antispam must be disabled when services cannot be discovered")
	}

	if err := ctl.ServiceStart(ctx, "antispam"); err != nil {
		t.Errorf("start: %v", err)
	}

	if err := ctl.ServiceStop(ctx, "antispam"); err != nil {
		t.Errorf("stop: %v", err)
	}

	if got := f.Exec.Lines(); len(got) != 0 {
		t.Errorf("no command may run for an idle service, got %v", got)
	}

	if ctl.ServiceDisabled(ctx, "store") || ctl.ServiceDisabled(ctx, "no-such") {
		t.Error("only IdleWhenDisabled services can be disabled")
	}
}
