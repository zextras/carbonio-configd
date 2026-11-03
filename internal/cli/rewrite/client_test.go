// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rewrite

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// listenLoopback listens on the loopback of network ("tcp4" or "tcp6") and
// answers every connection with reply after delay, recording the requests. It
// skips the test when the family is unavailable on this host.
func listenLoopback(t *testing.T, network, reply string, delay time.Duration) (port int, requests <-chan string) {
	t.Helper()

	host := "127.0.0.1"
	if network == networkTCP6 {
		host = "::1"
	}

	ln, err := net.Listen(network, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("%s loopback unavailable: %v", network, err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	got := make(chan string, 8)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				buf := make([]byte, 2048)

				n, _ := conn.Read(buf)
				got <- string(buf[:n])

				time.Sleep(delay)

				if reply != "" {
					_, _ = conn.Write([]byte(reply + "\n"))
				}
			}()
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, got
}

// closedPort returns a loopback port nothing listens on, on either family.
func closedPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	return port
}

func TestRequest_Unreachable(t *testing.T) {
	_, err := Request(t.Context(), "REWRITE", []string{"mta"}, Endpoint{Port: closedPort(t)})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestRequest_SendsCommandAndReturnsReply(t *testing.T) {
	port, requests := listenLoopback(t, networkTCP4, "SUCCESS REWRITES COMPLETE", 0)

	reply, err := Request(t.Context(), "REWRITE", []string{"mta", "proxy"}, Endpoint{Port: port})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	if reply != "SUCCESS REWRITES COMPLETE" {
		t.Errorf("reply = %q", reply)
	}

	if got := strings.TrimSpace(<-requests); got != "REWRITE mta proxy" {
		t.Errorf("request = %q, want %q", got, "REWRITE mta proxy")
	}
}

// TestRequest_WaitsForSlowReply: REWRITE replies only once the daemon has
// processed the sections, so a delayed reply is not a failure.
func TestRequest_WaitsForSlowReply(t *testing.T) {
	port, _ := listenLoopback(t, networkTCP4, "SUCCESS REWRITES COMPLETE", 300*time.Millisecond)

	start := time.Now()

	if _, err := Request(t.Context(), "REWRITE", []string{"mta"}, Endpoint{Port: port}); err != nil {
		t.Fatalf("Request: %v", err)
	}

	if time.Since(start) < 300*time.Millisecond {
		t.Error("returned before the daemon replied")
	}
}

func TestRequest_ErrorReply(t *testing.T) {
	port, _ := listenLoopback(t, networkTCP4, "ERROR REWRITE TIMEOUT", 0)

	reply, err := Request(t.Context(), "REWRITE", []string{"mta"}, Endpoint{Port: port})

	var replyErr *ReplyError
	if !errors.As(err, &replyErr) || replyErr.Reply != "ERROR REWRITE TIMEOUT" {
		t.Fatalf("err = %v, want *ReplyError ERROR REWRITE TIMEOUT", err)
	}

	if reply != "ERROR REWRITE TIMEOUT" {
		t.Errorf("reply = %q", reply)
	}

	if errors.Is(err, ErrUnreachable) {
		t.Error("an ERROR reply must not look like an unreachable daemon")
	}
}

// TestRequest_NoReplyIsNotUnreachable: a daemon that accepts the connection but
// does not answer is running; callers must not rewrite behind its back.
func TestRequest_NoReplyIsNotUnreachable(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		delay time.Duration
	}{
		{"closes without reply", "", 0},
		{"reply after the client timeout", "SUCCESS", time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, _ := listenLoopback(t, networkTCP4, tt.reply, tt.delay)

			_, err := Request(t.Context(), "REWRITE", []string{"mta"}, Endpoint{Port: port, Timeout: 150 * time.Millisecond})
			if err == nil {
				t.Fatal("Request succeeded without a reply")
			}

			if errors.Is(err, ErrUnreachable) {
				t.Errorf("err = %v, must not be ErrUnreachable", err)
			}
		})
	}
}

// TestRequest_FamilyFallback covers GCR-10: the daemon binds ::1 or 127.0.0.1
// by zimbraIPMode, which the client cannot read reliably, so whichever family
// the hint names the other is tried before declaring the daemon down.
func TestRequest_FamilyFallback(t *testing.T) {
	tests := []struct {
		name    string
		network string
		ipMode  string
	}{
		{"ipv6 daemon, no hint", networkTCP6, ""},
		{"ipv6 daemon, ipv4 hint", networkTCP6, IPModeIPv4},
		{"ipv4 daemon, ipv6 hint", networkTCP4, IPModeIPv6},
		{"ipv4 daemon, ipv4 hint", networkTCP4, IPModeIPv4},
		{"ipv6 daemon, ipv6 hint", networkTCP6, IPModeIPv6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, _ := listenLoopback(t, tt.network, "SUCCESS REWRITES COMPLETE", 0)

			if _, err := Request(t.Context(), "REWRITE", []string{"mta"}, Endpoint{Port: port, IPMode: tt.ipMode}); err != nil {
				t.Fatalf("Request: %v", err)
			}
		})
	}
}

func TestEndpointNetworks(t *testing.T) {
	tests := []struct {
		ipMode string
		want   []string
	}{
		{"", []string{networkTCP4, networkTCP6}},
		{IPModeIPv4, []string{networkTCP4, networkTCP6}},
		{"both", []string{networkTCP4, networkTCP6}},
		{IPModeIPv6, []string{networkTCP6, networkTCP4}},
	}

	for _, tt := range tests {
		got := Endpoint{IPMode: tt.ipMode}.networks()
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("networks(%q) = %v, want %v", tt.ipMode, got, tt.want)
		}
	}
}
