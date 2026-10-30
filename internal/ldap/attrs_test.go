// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseServerInheritedAttrs_RealExcerpt(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "attrs", "attrs.xml"))
	require.NoError(t, err)
	defer f.Close()

	names, err := ParseServerInheritedAttrs(f)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"zimbraMtaMyNetworks",
		"zimbraMilterBindPort",
		"zimbraMilterServerEnabled",
	}, names)
}

func TestParseServerInheritedAttrs_FlagListAndDedup(t *testing.T) {
	doc := `<attrs>
<attr name="a" flags="accountInfo,serverInherited,domainInherited"/>
<attr name="a" flags="serverInherited"/>
<attr name="b" flags="serverInheritedX"/>
<attr name="c"/>
</attrs>`
	names, err := ParseServerInheritedAttrs(strings.NewReader(doc))
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, names)
}

func TestParseServerInheritedAttrs_Malformed(t *testing.T) {
	_, err := ParseServerInheritedAttrs(strings.NewReader(`<attrs><attr name="a" flags="serverInherited"></attrs>`))
	require.Error(t, err)
}

func TestAttrsFileCache_ReparsesOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attrs.xml")
	require.NoError(t, os.WriteFile(path, []byte(`<attrs><attr name="a" flags="serverInherited"/></attrs>`), 0o600))

	var c attrsFileCache

	names, err := c.serverInheritedAttrs(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, names)

	require.NoError(t, os.WriteFile(path, []byte(`<attrs><attr name="b" flags="serverInherited"/><attr name="c" flags="serverInherited"/></attrs>`), 0o600))

	names, err = c.serverInheritedAttrs(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, names)
}
