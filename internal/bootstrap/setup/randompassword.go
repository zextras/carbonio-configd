// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The alphabets of com.zimbra.common.util.RandomPassword: 64 characters, or
// 63 without the dot (-l), which a local part must not begin with.
const (
	randomAlphabet       = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_."
	randomAlphabetNoDot  = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
	randomDefaultMin     = 24
	randomDefaultMax     = 32
	randomUsage          = "\nRandomPassword [-l] <minLength> <maxLength>\n"
	randomExitUsage      = 1
	randomExitBadLengths = 1
)

// RunRandomPassword is "RandomPassword [-l] [<minLength> <maxLength>]": it
// prints a password of a length between the two (24 and 32 when they are not
// given) made of characters drawn from rnd, a cryptographic source. -l is
// "localpart": the alphabet has no dot. Like the Java class, an argument that
// is not a number is reported and the default stays in its place; min above
// max is refused.
func RunRandomPassword(args []string, rnd io.Reader, stdout, stderr io.Writer) int {
	localpart := false

	for len(args) > 0 && (args[0] == "-l" || args[0] == "--localpart") {
		localpart = true
		args = args[1:]
	}

	minLen, maxLen := randomDefaultMin, randomDefaultMax

	if len(args) != 0 {
		if len(args) != 2 {
			_, _ = io.WriteString(stdout, randomUsage)

			return randomExitUsage
		}

		minLen = lengthArg(args[0], minLen, stderr)
		maxLen = lengthArg(args[1], maxLen, stderr)
	}

	alphabet := randomAlphabet
	if localpart {
		alphabet = randomAlphabetNoDot
	}

	pw, err := randomPassword(rnd, minLen, maxLen, alphabet)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)

		return randomExitBadLengths
	}

	_, _ = fmt.Fprintln(stdout, pw)

	return 0
}

// lengthArg reads a length; a value that is not an integer is reported and def
// is kept (the catch block of RandomPassword.main).
func lengthArg(arg string, def int, stderr io.Writer) int {
	n, err := strconv.ParseInt(arg, 10, 32)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid length: %q\n", arg)

		return def
	}

	return int(n)
}

// randomPassword is RandomPassword.generate: a length from min to max, each
// character picked by a random byte, one that falls in the incomplete last
// round of the alphabet being discarded so that no character is likelier.
func randomPassword(rnd io.Reader, minLen, maxLen int, alphabet string) (string, error) {
	if minLen > maxLen {
		return "", fmt.Errorf("minLength=%d > maxLength=%d", minLen, maxLen)
	}

	length := maxLen

	if minLen < maxLen {
		n, err := randomBelow(rnd, uint32(maxLen-minLen+1)) //nolint:gosec // maxLen > minLen, a positive int32 range
		if err != nil {
			return "", err
		}

		length = minLen + int(n)
	}

	limit := 256 - (256 % len(alphabet))

	var pw strings.Builder

	one := make([]byte, 1)

	for pw.Len() < length {
		if _, err := io.ReadFull(rnd, one); err != nil {
			return "", fmt.Errorf("reading random bytes: %w", err)
		}

		if i := int(one[0]); i < limit {
			pw.WriteByte(alphabet[i%len(alphabet)])
		}
	}

	return pw.String(), nil
}

// randomBelow is a uniform number in [0, bound), by rejection of the values
// above the largest multiple of bound.
func randomBelow(rnd io.Reader, bound uint32) (uint32, error) {
	limit := ^uint32(0) - (^uint32(0)%bound+1)%bound

	var buf [4]byte

	for {
		if _, err := io.ReadFull(rnd, buf[:]); err != nil {
			return 0, fmt.Errorf("reading random bytes: %w", err)
		}

		if v := binary.BigEndian.Uint32(buf[:]); v <= limit {
			return v % bound, nil
		}
	}
}
