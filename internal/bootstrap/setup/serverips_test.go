// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// ipAddrOutput is the test data of zmserverips, with the lines "ip addr" adds.
const ipAddrOutput = `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
    inet6 ::1/128 scope host
ignored stuff
    inet 10.0.0.1/24 brd 10.0.0.255 scope global br0
    inet 10.1.2.3 peer 10.1.2.9/32 brd 10.1.2.255 scope global eth0
    inet6 2001:db8::6/48 scope link
    inet6 2001:db8::1/64 scope global
    inet 192.0.0.9/17 scope global
    inet 10.9.9.9/0 scope global
`

func serverIPsRun(t *testing.T, reply systest.Handler, args ...string) (status int, out, errOut string) {
	t.Helper()

	fakes := systest.New()
	fakes.Exec.OnCmdline("/sbin/ip addr", reply)

	var o, e bytes.Buffer

	status = RunServerIPs(context.Background(), fakes.Exec, args, &o, &e)

	return status, o.String(), e.String()
}

func TestRunServerIPs_ListsAddressesAndMasksWithoutLinkLocal(t *testing.T) {
	status, out, _ := serverIPsRun(t, systest.Reply(ipAddrOutput))

	assert.Equal(t, 0, status)
	assert.Equal(t, "addr:127.0.0.1 Mask:255.0.0.0\n"+
		"addr:::1 Mask:128\n"+
		"addr:10.0.0.1 Mask:255.255.255.0\n"+
		"addr:10.1.2.3 Mask:255.255.255.255\n"+
		"addr:2001:db8::1 Mask:64\n"+
		"addr:192.0.0.9 Mask:255.255.128.0\n"+
		"addr:10.9.9.9 Mask:0.0.0.0\n", out)
}

func TestRunServerIPs_NetworksPrintsTheNetworksForMyNetworks(t *testing.T) {
	for _, opt := range []string{"-n", "--networks", "--net"} {
		status, out, _ := serverIPsRun(t, systest.Reply(ipAddrOutput), opt)

		assert.Equal(t, 0, status, opt)
		assert.Equal(t, "127.0.0.0/8\n[::1]/128\n10.0.0.0/24\n10.1.2.3/32\n[2001:db8::]/64\n192.0.0.0/17\n0.0.0.0/0\n", out, opt)
	}
}

func TestRunServerIPs_StatusAndErrors(t *testing.T) {
	status, out, _ := serverIPsRun(t, systest.Reply("1: lo\n    inet6 fe80::1/64 scope link\n"))
	assert.Equal(t, 1, status, "no address, no success")
	assert.Empty(t, out)

	status, _, errOut := serverIPsRun(t, systest.Fail(1, "boom"))
	assert.Equal(t, 2, status)
	assert.Contains(t, errOut, "unable to execute: /sbin/ip")

	status, out, errOut = serverIPsRun(t, systest.Reply(ipAddrOutput), "extra")
	assert.Equal(t, 1, status)
	assert.Contains(t, errOut, "unknown arguments: extra")
	assert.Contains(t, out, "Usage: zmserverips")

	status, out, _ = serverIPsRun(t, systest.Reply(ipAddrOutput), "--help")
	assert.Equal(t, 1, status)
	assert.Contains(t, out, "--networks")

	status, _, errOut = serverIPsRun(t, systest.Reply(ipAddrOutput), "--bogus")
	assert.Equal(t, 2, status)
	assert.Contains(t, errOut, "Usage: zmserverips")
}

func TestParseServerIPs_SkipsTheLinesItCannotRead(t *testing.T) {
	ifs := parseServerIPs(bytes.NewBufferString("    inet\n    inet nonsense/8\n    inet 1.2.3.4/99\n    inet 1.2.3.4/x\n    inet 1.2.3.4/8\n"), false)

	assert.Equal(t, []serverIf{{"1.2.3.4", "255.0.0.0"}}, ifs)
}
