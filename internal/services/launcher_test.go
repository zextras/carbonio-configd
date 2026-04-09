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
)

// --- buildLDAPBindURL ---

func TestBuildLDAPBindURL_ExplicitBindURL(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"ldap_bind_url": "ldap://myldap.example.com:389",
	}
	got := buildLDAPBindURL(lc)
	if got != "ldap://myldap.example.com:389" {
		t.Errorf("expected explicit bind URL, got %q", got)
	}
}

func TestBuildLDAPBindURL_MultipleBindURLs(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Multiple space-separated URLs: return all of them (preserves spaces).
	lc := map[string]string{
		"ldap_bind_url": "ldap://host1:389 ldap://host2:389",
	}
	got := buildLDAPBindURL(lc)
	if got != "ldap://host1:389 ldap://host2:389" {
		t.Errorf("expected full bind URL string, got %q", got)
	}
}

func TestBuildLDAPBindURL_FallbackToLdapURL(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"ldap_url": "ldap://primary:389 ldap://secondary:389",
	}
	got := buildLDAPBindURL(lc)
	// Falls back to first URL only.
	if got != "ldap://primary:389" {
		t.Errorf("expected first URL from ldap_url, got %q", got)
	}
}

func TestBuildLDAPBindURL_ReconstructFromHostPort(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"zimbra_server_hostname": "myserver.example.com",
		"ldap_port":              "636",
	}
	got := buildLDAPBindURL(lc)
	if got != "ldap://myserver.example.com:636" {
		t.Errorf("expected reconstructed URL, got %q", got)
	}
}

func TestBuildLDAPBindURL_DefaultsWhenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	got := buildLDAPBindURL(lc)
	if got != "ldap://localhost:389" {
		t.Errorf("expected default URL ldap://localhost:389, got %q", got)
	}
}

func TestBuildLDAPBindURL_DefaultPort(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"zimbra_server_hostname": "myhost",
	}
	got := buildLDAPBindURL(lc)
	if got != "ldap://myhost:389" {
		t.Errorf("expected default port 389, got %q", got)
	}
}

func TestBuildLDAPBindURL_DefaultHost(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"ldap_port": "636",
	}
	got := buildLDAPBindURL(lc)
	if got != "ldap://localhost:636" {
		t.Errorf("expected default host localhost, got %q", got)
	}
}

// --- mailboxJavaArgs ---

func TestMailboxJavaArgs_DefaultHeapAndStack(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := mailboxJavaArgs(lc)

	// Must contain -Xms512m and -Xmx512m defaults.
	assertContains(t, args, "-Xms512m")
	assertContains(t, args, "-Xmx512m")

	// Must contain default thread stack
	assertContainsPrefix(t, args, "-Xss")

	// UTF-8 encoding flag must be first
	if len(args) == 0 || args[0] != "-Dfile.encoding=UTF-8" {
		t.Errorf("expected first arg to be -Dfile.encoding=UTF-8, got %v", args)
	}

	// Must end with the main class
	assertContains(t, args, "com.zextras.mailbox.Mailbox")
}

func TestMailboxJavaArgs_CustomHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"mailboxd_java_heap_size": "2048",
	}
	args := mailboxJavaArgs(lc)
	assertContains(t, args, "-Xms2048m")
	assertContains(t, args, "-Xmx2048m")
}

func TestMailboxJavaArgs_CustomThreadStack(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"mailboxd_thread_stack_size": "512k",
	}
	args := mailboxJavaArgs(lc)
	assertContains(t, args, "-Xss512k")
}

func TestMailboxJavaArgs_NetworkTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"networkaddress_cache_ttl": "30",
	}
	args := mailboxJavaArgs(lc)
	assertContainsPrefix(t, args, "-Dsun.net.inetaddr.ttl=30")
}

func TestMailboxJavaArgs_DefaultNetworkTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := mailboxJavaArgs(lc)
	assertContainsPrefix(t, args, "-Dsun.net.inetaddr.ttl=60")
}

func TestMailboxJavaArgs_Log4jProps(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"zimbra_log4j_properties": "/etc/carbonio/log4j.xml",
	}
	args := mailboxJavaArgs(lc)
	assertContainsPrefix(t, args, "-Dlog4j.configurationFile=")
}

func TestMailboxJavaArgs_GcLogStripped(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// GC log flag must be stripped from java options.
	gcFlag := "-Xlog:gc*=info,safepoint=info:file=" + logPath + "/gc.log:time:filecount=20,filesize=10m"
	lc := map[string]string{
		"mailboxd_java_options": gcFlag + " -Xss256k",
	}
	args := mailboxJavaArgs(lc)
	for _, a := range args {
		if strings.Contains(a, "gc*=info") {
			t.Errorf("GC log flag should be stripped from args, found: %q", a)
		}
	}
}

func TestMailboxJavaArgs_NoXssDoubleInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// If java options already contain Xss, don't add another.
	lc := map[string]string{
		"mailboxd_java_options": "-Xss1m",
	}
	args := mailboxJavaArgs(lc)
	count := 0
	for _, a := range args {
		if strings.HasPrefix(a, "-Xss") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 -Xss arg, got %d", count)
	}
}

func TestMailboxJavaArgs_ContainsCpAndMainClass(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := mailboxJavaArgs(lc)
	assertContains(t, args, "-cp")
	assertContains(t, args, "com.zextras.mailbox.Mailbox")
}

// --- milterJavaArgs ---

func TestMilterJavaArgs_DefaultJavaOpts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := milterJavaArgs(lc)

	// First arg must be -client
	if len(args) == 0 || args[0] != "-client" {
		t.Errorf("expected first arg -client, got %v", args)
	}

	// Must contain -Xmx256m from defaults
	assertContains(t, args, "-Xmx256m")
}

func TestMilterJavaArgs_CustomJavaOpts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"zimbra_zmjava_options": "-Xmx512m -Xms128m",
	}
	args := milterJavaArgs(lc)
	assertContains(t, args, "-Xmx512m")
	assertContains(t, args, "-Xms128m")
}

func TestMilterJavaArgs_CustomLibPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{
		"zimbra_zmjava_java_library_path": "/custom/lib",
	}
	args := milterJavaArgs(lc)
	assertContainsPrefix(t, args, "-Djava.library.path=/custom/lib")
}

func TestMilterJavaArgs_DefaultLibPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := milterJavaArgs(lc)
	assertContainsPrefix(t, args, "-Djava.library.path=")
}

func TestMilterJavaArgs_ContainsMainClass(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := milterJavaArgs(lc)
	assertContains(t, args, "com.zimbra.cs.milter.MilterServer")
}

func TestMilterJavaArgs_ContainsClasspath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := milterJavaArgs(lc)
	assertContains(t, args, "-classpath")
}

func TestMilterJavaArgs_ContainsZimbraHome(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	lc := map[string]string{}
	args := milterJavaArgs(lc)
	assertContainsPrefix(t, args, "-Dzimbra.home=")
}

func TestMilterJavaArgs_TLSProtocols(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Default opts must contain TLS settings.
	lc := map[string]string{}
	args := milterJavaArgs(lc)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "TLSv1") {
		t.Error("expected TLS protocol flags in default milter args")
	}
}

// --- sortByOrder / orderOf ---

func TestOrderOf_KnownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	order := map[string]int{"ldap": 0, "mailbox": 50, "mta": 150}

	if got := orderOf("ldap", order); got != 0 {
		t.Errorf("orderOf(ldap) = %d, want 0", got)
	}
	if got := orderOf("mailbox", order); got != 50 {
		t.Errorf("orderOf(mailbox) = %d, want 50", got)
	}
	if got := orderOf("mta", order); got != 150 {
		t.Errorf("orderOf(mta) = %d, want 150", got)
	}
}

func TestOrderOf_UnknownServiceReturns1000(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	order := map[string]int{"ldap": 0}
	if got := orderOf("unknown-svc", order); got != 1000 {
		t.Errorf("orderOf(unknown) = %d, want 1000", got)
	}
}

func TestSortByOrder_SortsCorrectly(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	order := map[string]int{"ldap": 0, "mailbox": 50, "proxy": 70, "mta": 150}
	names := []string{"mta", "proxy", "ldap", "mailbox"}
	sortByOrder(names, order)

	expected := []string{"ldap", "mailbox", "proxy", "mta"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("sorted[%d] = %q, want %q", i, names[i], want)
		}
	}
}

func TestSortByOrder_UnknownServicesAppended(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	order := map[string]int{"ldap": 0}
	names := []string{"unknown2", "ldap", "unknown1"}
	sortByOrder(names, order)

	if names[0] != "ldap" {
		t.Errorf("expected ldap first, got %q", names[0])
	}
	// unknown1 and unknown2 both get order 1000, sorted alphabetically
	if names[1] != "unknown1" || names[2] != "unknown2" {
		t.Errorf("expected unknown services sorted alphabetically at end, got %v", names[1:])
	}
}

func TestSortByOrder_EqualOrderAlphabetical(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	order := map[string]int{"beta": 10, "alpha": 10}
	names := []string{"beta", "alpha"}
	sortByOrder(names, order)

	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("expected alphabetical order for equal-ordered services, got %v", names)
	}
}

// --- IsCustomEnabled ---

func TestIsCustomEnabled_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsCustomEnabled(context.Background(), "nonexistent-service") {
		t.Error("expected false for unknown service")
	}
}

func TestIsCustomEnabled_ServiceWithoutEnableCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// mta has no EnableCheck
	if IsCustomEnabled(context.Background(), "mta") {
		t.Error("expected false for service without EnableCheck")
	}
}

func TestIsCustomEnabled_MilterWithEnableCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// milter has an EnableCheck (milterEnabled); we can test it without caring about the result
	// — just verify IsCustomEnabled doesn't panic and reads EnableCheck.
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "mta_milter_options")

	oldPath := milterOptionsPath
	milterOptionsPath = filePath
	defer func() { milterOptionsPath = oldPath }()

	// Write enabled file
	if err := os.WriteFile(filePath, []byte("zimbraMilterServerEnabled=TRUE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !IsCustomEnabled(context.Background(), "milter") {
		t.Error("expected true when milter options file says enabled")
	}

	// Write disabled file
	if err := os.WriteFile(filePath, []byte("zimbraMilterServerEnabled=FALSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if IsCustomEnabled(context.Background(), "milter") {
		t.Error("expected false when milter options file says disabled")
	}
}

// --- validateName (remote.go) ---

func TestValidateName_ValidNames(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	valid := []string{"mta", "proxy", "mailbox", "service-discover", "my_service", "SVC123", "a"}
	for _, name := range valid {
		if err := validateName(name, "service"); err != nil {
			t.Errorf("validateName(%q) returned unexpected error: %v", name, err)
		}
	}
}

func TestValidateName_InvalidNames(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	invalid := []string{
		"",
		"svc name",      // space
		"svc;cmd",       // semicolon
		"svc&&cmd",      // shell injection
		"../etc/passwd", // path traversal
		"svc\ncmd",      // newline
		"svc$var",       // dollar sign
	}
	for _, name := range invalid {
		if err := validateName(name, "service"); err == nil {
			t.Errorf("validateName(%q) expected error, got nil", name)
		}
	}
}

func TestValidateName_ErrorMentionsKind(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := validateName("bad name!", "action")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "action") {
		t.Errorf("expected error to mention kind 'action', got: %v", err)
	}
}

// --- killStatsPidFile ---

func TestKillStatsPidFile_MissingFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctx := context.Background()
	result := killStatsPidFile(ctx, "/nonexistent/path/zmstat-cpu.pid")
	if result {
		t.Error("expected false for missing pid file")
	}
}

func TestKillStatsPidFile_InvalidPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "zmstat-cpu.pid")
	if err := os.WriteFile(pidFile, []byte("notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	result := killStatsPidFile(ctx, pidFile)
	if result {
		t.Error("expected false for invalid pid")
	}

	// File should be removed even on parse error
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after invalid pid")
	}
}

// --- helpers ---

func assertContains(t *testing.T, args []string, needle string) {
	t.Helper()
	for _, a := range args {
		if a == needle {
			return
		}
	}
	t.Errorf("expected args to contain %q, got %v", needle, args)
}

func assertContainsPrefix(t *testing.T, args []string, prefix string) {
	t.Helper()
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return
		}
	}
	t.Errorf("expected args to contain element with prefix %q, got %v", prefix, args)
}
