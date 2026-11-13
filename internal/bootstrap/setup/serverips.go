// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/zextras/carbonio-configd/internal/getopt"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// serverIPsCommand is the command whose output zmserverips reads.
const serverIPsCommand = "/sbin/ip"

const serverIPsUsage = `zmserverips - enumerate local server IP addresses for validation purposes

Usage: zmserverips [options]

    --help                      display a brief help message
    --man                       display full documentation
    --networks                  generate mynetworks compatible interface data
`

// Exit statuses of zmserverips: no address found, a refused command line is 2
// like pod2usage, the ip command missing 2 as well.
const (
	serverIPsNone    = 1
	serverIPsFailure = 2
)

var serverIPsSpecs = []getopt.Spec{
	{Names: []string{"help"}},
	{Names: []string{"man"}},
	{Names: []string{"networks"}},
}

// serverIf is an address of the server: what the first column of a line of
// zmserverips is, and its mask (dotted for IPv4, the prefix length for IPv6).
type serverIf struct{ addr, mask string }

// RunServerIPs is zmserverips: it lists the addresses of the interfaces
// (/sbin/ip addr, without the link-local ones) as "addr:A Mask:M", or with
// --networks as the networks, "10.1.5.0/24" and "[::1]/128" (mynetworks). The
// status is 0 when at least one address was listed.
func RunServerIPs(ctx context.Context, x sys.Exec, args []string, stdout, stderr io.Writer) int {
	o := getopt.Parse(args, serverIPsSpecs, stderr)
	if !o.AllGood {
		_, _ = io.WriteString(stderr, serverIPsUsage)

		return serverIPsFailure
	}

	if len(o.Rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "serverips: unknown arguments: %s\n", strings.Join(o.Rest, " "))
	}

	if o.Has("help") || o.Has("man") || len(o.Rest) > 0 {
		_, _ = io.WriteString(stdout, serverIPsUsage)

		return serverIPsNone
	}

	out, err := sys.Output(ctx, x, serverIPsCommand, "addr")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "serverips: unable to execute: %s\n", serverIPsCommand)

		return serverIPsFailure
	}

	ifs := parseServerIPs(strings.NewReader(string(out)), o.Has("networks"))

	for _, i := range ifs {
		if o.Has("networks") {
			_, _ = fmt.Fprintln(stdout, i.addr)
		} else {
			_, _ = fmt.Fprintf(stdout, "addr:%s Mask:%s\n", i.addr, i.mask)
		}
	}

	if len(ifs) == 0 {
		return serverIPsNone
	}

	return 0
}

// parseServerIPs reads the output of "ip addr": the "inet" and "inet6" lines
// that are not "scope link". With networks the address is replaced by its
// network.
func parseServerIPs(r io.Reader, networks bool) []serverIf {
	var ifs []serverIf

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()

		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "inet") || strings.Contains(line, "scope link") {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}

		if i, ok := serverInterface(fields[0], fields[1], networks); ok {
			ifs = append(ifs, i)
		}
	}

	return ifs
}

// serverInterface converts the type ("inet", "inet6") and the address with its
// prefix of one line.
func serverInterface(kind, ipmask string, networks bool) (serverIf, bool) {
	ip, bits, hasBits := strings.Cut(ipmask, "/")

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return serverIf{}, false
	}

	prefix := addr.BitLen()

	if hasBits {
		if _, err := fmt.Sscanf(bits, "%d", &prefix); err != nil || prefix < 0 || prefix > addr.BitLen() {
			return serverIf{}, false
		}
	}

	net := netip.PrefixFrom(addr, prefix).Masked()
	mask := fmt.Sprint(prefix)

	if kind == "inet" {
		mask = dottedMask(prefix)
	}

	switch {
	case !networks:
		return serverIf{ip, mask}, true
	case kind == "inet":
		return serverIf{net.String(), mask}, true
	default:
		return serverIf{"[" + net.Addr().String() + "]/" + mask, mask}, true
	}
}

// dottedMask is the netmask of an IPv4 prefix length.
func dottedMask(prefix int) string {
	m := ^uint32(0) << (32 - prefix) // a shift of 32 gives 0, the mask of /0

	return fmt.Sprintf("%d.%d.%d.%d", m>>24, m>>16&0xff, m>>8&0xff, m&0xff)
}
