// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"errors"
	"log/syslog"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Zextras::Mon::Logger::Log (40-56): "<pid>:<level>: <message>", and a message
// of more than 800 characters in pieces of 800 that name each other with
// :::<uuid>::: markers.
func TestEmit(t *testing.T) {
	t.Parallel()

	var sent []string

	send := func(m string) { sent = append(sent, m) }

	emit(send, 7, "info", "hello")
	assert.Equal(t, []string{"7:info: hello"}, sent)

	sent = nil
	body := strings.Repeat("a", 800) + strings.Repeat("b", 800) + strings.Repeat("c", 400)
	emit(send, 7, "err", body)

	require.Len(t, sent, 3)

	marker := regexp.MustCompile(`:::([0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}):::`)
	first := marker.FindStringSubmatch(sent[0])
	second := marker.FindAllStringSubmatch(sent[1], -1)

	require.NotNil(t, first)
	require.Len(t, second, 2)
	assert.Equal(t, "7:err: "+strings.Repeat("a", 800)+first[0], sent[0])
	assert.Equal(t, first[1], second[0][1], "the second piece names the first")
	assert.Equal(t, "7:err: "+first[0]+strings.Repeat("b", 800)+second[1][0], sent[1])
	assert.Equal(t, second[1][0]+strings.Repeat("c", 400), sent[2])
}

func TestEmitExactlyOneChunkIsNotSplit(t *testing.T) {
	t.Parallel()

	var sent []string

	emit(func(m string) { sent = append(sent, m) }, 1, "info", strings.Repeat("x", 800))

	assert.Equal(t, []string{"1:info: " + strings.Repeat("x", 800)}, sent)
}

// listenSyslog is a UDP syslog collector; dial connects a writer to it.
func listenSyslog(t *testing.T) (dial func(syslog.Priority, string) (*syslog.Writer, error), recv func() string) {
	t.Helper()

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	dial = func(p syslog.Priority, tag string) (*syslog.Writer, error) {
		return syslog.Dial("udp", conn.LocalAddr().String(), p, tag)
	}
	recv = func() string {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

		buf := make([]byte, 2048)
		n, _, err := conn.ReadFrom(buf)
		require.NoError(t, err)

		return string(buf[:n])
	}

	return dial, recv
}

// A syslog that cannot be reached is silent, and is tried again on the next
// message rather than being given up for the life of the process.
//
//nolint:paralleltest // swaps the package-level dialer
func TestSystemSyslogRetriesAfterAFailedConnection(t *testing.T) {
	dial, recv := listenSyslog(t)
	orig := dialSyslog

	t.Cleanup(func() { dialSyslog = orig })

	calls := 0
	dialSyslog = func(p syslog.Priority, tag string) (*syslog.Writer, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("no syslog socket")
		}

		return dial(p, tag)
	}

	s := NewSystemSyslog(9)
	s.Log("info", "lost")
	s.Log("info", "delivered")

	assert.Equal(t, 2, calls)
	assert.Contains(t, recv(), "9:info: delivered")
}

// One connection per facility serves every later message.
//
//nolint:paralleltest // swaps the package-level dialer
func TestSystemSyslogReusesTheConnectionOfAFacility(t *testing.T) {
	dial, recv := listenSyslog(t)
	orig := dialSyslog

	t.Cleanup(func() { dialSyslog = orig })

	calls := 0
	dialSyslog = func(p syslog.Priority, tag string) (*syslog.Writer, error) {
		calls++

		return dial(p, tag)
	}

	s := NewSystemSyslog(3)
	s.Log("err", "one")
	s.Log("debug", "two")
	s.Stats("crit", "three")

	assert.Contains(t, recv(), "3:err: one")
	assert.Contains(t, recv(), "3:debug: two")
	assert.Contains(t, recv(), "3:crit: three")
	assert.Equal(t, 2, calls, "local0 and local1 are opened once each")
}
