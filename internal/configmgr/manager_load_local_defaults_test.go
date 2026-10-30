// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

// Legacy getlocal (jylibs/commands.py) returns LC.get for every LC.getAllKeys
// key: LC.java defaults merged under localconfig.xml, ${var} expanded. A key
// absent from localconfig.xml but known to LC.java must therefore resolve.
func TestExecuteLocalConfigCommand_IncludesDefaults(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
<key name="zimbra_home"><value>/opt/zextras</value></key>
</localconfig>`
	path := filepath.Join(t.TempDir(), "localconfig.xml")

	if err := os.WriteFile(path, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}

	prev := loadResolvedLocalConfig
	t.Cleanup(func() { loadResolvedLocalConfig = prev })

	loadResolvedLocalConfig = func() (map[string]string, error) {
		return localconfig.LoadResolvedConfigFromFile(path)
	}

	cm := newTestConfigManager(t)

	out, err := cm.executeLocalConfigCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := "antispam_mysql_data_directory = /opt/zextras/data/amavisd/mysql/data"
	if !strings.Contains(out, want) {
		t.Errorf("default with expanded ${zimbra_home} missing: want line %q", want)
	}
}
