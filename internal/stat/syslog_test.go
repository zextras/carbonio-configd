// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"regexp"
	"strings"
	"testing"

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
