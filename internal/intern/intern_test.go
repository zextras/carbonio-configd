// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package intern_test

import (
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/zextras/carbonio-configd/internal/intern"
)

// stringData returns the address of the string's underlying byte storage.
// Two strings with the same data pointer are guaranteed to share storage.
func stringData(s string) uintptr {
	if s == "" {
		return 0
	}

	return uintptr(unsafe.Pointer(unsafe.StringData(s)))
}

func TestKey_RoundTripsValue(t *testing.T) {
	for _, in := range []string{"", "zimbra_server_hostname", "ldap_is_master", "antispam_enable_dcc"} {
		got := intern.Key(in)
		if got != in {
			t.Fatalf("Key(%q) = %q; want %q", in, got, in)
		}
	}
}

func TestKey_SameInputSameBackingStorage(t *testing.T) {
	const input = "ldap_master_url"

	a := intern.Key(input)
	b := intern.Key(input)

	if a != b {
		t.Fatalf("Key values differ: %q vs %q", a, b)
	}

	// Allocate a fresh copy of the same logical string so the comparison below
	// exercises pointer-identity, not compile-time constant folding.
	freshCopy := strings.Clone(input)

	c := intern.Key(freshCopy)

	if stringData(a) != stringData(c) {
		t.Fatalf("Key did not canonicalise storage: %#x vs %#x", stringData(a), stringData(c))
	}
}

func TestAttr_SameInputSameBackingStorage(t *testing.T) {
	const input = "zimbraServiceEnabled"

	a := intern.Attr(input)

	freshCopy := strings.Clone(input)

	b := intern.Attr(freshCopy)

	if stringData(a) != stringData(b) {
		t.Fatalf("Attr did not canonicalise storage: %#x vs %#x", stringData(a), stringData(b))
	}
}

func TestService_SameInputSameBackingStorage(t *testing.T) {
	const input = "mta"

	a := intern.Service(input)

	freshCopy := strings.Clone(input)

	b := intern.Service(freshCopy)

	if stringData(a) != stringData(b) {
		t.Fatalf("Service did not canonicalise storage: %#x vs %#x", stringData(a), stringData(b))
	}
}

func TestCategoriesDoNotShareStorage(t *testing.T) {
	const input = "ldap"

	key := intern.Key(input)
	attr := intern.Attr(input)
	svc := intern.Service(input)

	if stringData(key) == stringData(attr) {
		t.Fatalf("Key and Attr share storage for %q; categories must be isolated", input)
	}

	if stringData(key) == stringData(svc) {
		t.Fatalf("Key and Service share storage for %q; categories must be isolated", input)
	}

	if stringData(attr) == stringData(svc) {
		t.Fatalf("Attr and Service share storage for %q; categories must be isolated", input)
	}
}

func TestConcurrentCallersShareHandles(t *testing.T) {
	const (
		workers  = 32
		perWorker = 1000
	)

	inputs := [...]string{"amavis", "mta", "proxy", "mailbox", "ldap", "antispam", "antivirus", "opendkim"}

	var (
		wg       sync.WaitGroup
		addr     [len(inputs)]uintptr
		addrOnce [len(inputs)]sync.Once
	)

	wg.Add(workers)

	for range workers {
		go func() {
			defer wg.Done()

			for range perWorker {
				for i, in := range inputs {
					got := intern.Service(in)
					if got != in {
						t.Errorf("Service(%q) = %q", in, got)

						return
					}

					current := stringData(got)

					addrOnce[i].Do(func() {
						addr[i] = current
					})

					if addr[i] != current {
						t.Errorf("Service(%q) storage drift: first %#x, now %#x", in, addr[i], current)

						return
					}
				}
			}
		}()
	}

	wg.Wait()
}
