// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counting is a source of the bytes 0, 1, 2... (wrapping), so the password it
// yields is known.
type counting struct{ next byte }

func (c *counting) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = c.next
		c.next++
	}

	return len(p), nil
}

func randomPasswordRun(rnd io.Reader, args ...string) (status int, out, errOut string) {
	var o, e bytes.Buffer

	status = RunRandomPassword(args, rnd, &o, &e)

	return status, o.String(), e.String()
}

func TestRunRandomPassword_LengthIsBetweenMinAndMaxAndUsesTheAlphabet(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		min, max int
		alphabet string
	}{
		{[]string{"8", "10"}, 8, 10, randomAlphabet},
		{[]string{"-l", "8", "10"}, 8, 10, randomAlphabetNoDot},
		{[]string{"5", "5"}, 5, 5, randomAlphabet},
		{nil, 24, 32, randomAlphabet},
	} {
		seen := map[int]bool{}

		for range 200 {
			status, out, errOut := randomPasswordRun(rand.Reader, tc.args...)
			require.Equal(t, 0, status, errOut)

			pw := strings.TrimSuffix(out, "\n")
			require.GreaterOrEqual(t, len(pw), tc.min)
			require.LessOrEqual(t, len(pw), tc.max)
			assert.Empty(t, strings.Trim(pw, tc.alphabet), "only characters of the alphabet")

			seen[len(pw)] = true
		}

		assert.Len(t, seen, tc.max-tc.min+1, "every length is drawn for %v", tc.args)
	}
}

func TestRunRandomPassword_TheLocalPartNeverHasADot(t *testing.T) {
	for range 100 {
		_, out, _ := randomPasswordRun(rand.Reader, "-l", "40", "40")
		assert.NotContains(t, out, ".")
	}
}

func TestRunRandomPassword_BytesAboveTheLimitAreDiscarded(t *testing.T) {
	// 64 characters divide 256: no byte is discarded, byte i is alphabet[i%64].
	_, out, _ := randomPasswordRun(&counting{}, "6", "6")
	assert.Equal(t, "012345\n", out)

	// 63 characters: the bytes 252..255 are dropped.
	_, out, _ = randomPasswordRun(&counting{next: 250}, "-l", "6", "6")
	assert.Equal(t, string([]byte{randomAlphabetNoDot[250%63], randomAlphabetNoDot[251%63], randomAlphabetNoDot[0],
		randomAlphabetNoDot[1], randomAlphabetNoDot[2], randomAlphabetNoDot[3]})+"\n", out)
}

func TestRunRandomPassword_UsageAndErrors(t *testing.T) {
	status, out, _ := randomPasswordRun(rand.Reader, "8")
	assert.Equal(t, 1, status)
	assert.Equal(t, "\nRandomPassword [-l] <minLength> <maxLength>\n", out)

	status, out, errOut := randomPasswordRun(rand.Reader, "10", "8")
	assert.Equal(t, 1, status)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "minLength=10 > maxLength=8")

	// A length that is not a number is reported and the default stays.
	status, out, errOut = randomPasswordRun(rand.Reader, "x", "30")
	assert.Equal(t, 0, status)
	assert.Contains(t, errOut, `invalid length: "x"`)
	assert.GreaterOrEqual(t, len(out)-1, 24)
	assert.LessOrEqual(t, len(out)-1, 30)

	status, _, errOut = randomPasswordRun(iotest.ErrReader(io.ErrUnexpectedEOF), "8", "10")
	assert.Equal(t, 1, status)
	assert.Contains(t, errOut, "reading random bytes")

	status, _, errOut = randomPasswordRun(iotest.ErrReader(io.ErrUnexpectedEOF), "8", "8")
	assert.Equal(t, 1, status)
	assert.Contains(t, errOut, "reading random bytes")
}
