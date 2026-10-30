// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
)

func parseCfForParity(t *testing.T, content string, enabledServices ...string) *ConfigManager {
	t.Helper()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "zmconfigd.cf")
	require.NoError(t, os.WriteFile(cfgFile, []byte(content), 0o644))

	cm := setupTestConfigManagerForParser(dir)
	for _, svc := range enabledServices {
		cm.State.ServerConfig.ServiceConfig.Set(svc, "TRUE")
	}

	require.NoError(t, cm.ParseMtaConfig(context.Background(), cfgFile))

	return cm
}

// jylibs/mtaconfig.py load: a section whose mapped service is disabled on the
// node is skipped entirely, so it must not exist for any consumer (compare,
// rewrites, mapfiles, restarts, dependencies).
func TestParseMtaConfig_DropsSectionsOfDisabledServices(t *testing.T) {
	t.Parallel()

	const cf = `SECTION dhparam
	MAPFILE zimbraSSLDHParam
SECTION amavis
	VAR zimbraAmavisMaxServers
	RESTART amavis
SECTION mta DEPENDS amavis
	VAR zimbraMtaMyNetworks
	RESTART mta
SECTION proxy
	VAR zimbraReverseProxyHttpPort
	RESTART proxy
SECTION webxml
	VAR zimbraMailPort
SECTION nginx
	VAR zimbraReverseProxyLookupTarget
SECTION cbpolicyd
	VAR zimbraCBPolicydBindPort
`

	tests := []struct {
		name    string
		enabled []string
		want    []string
	}{
		{"nothing enabled keeps only negated dhparam", nil, []string{"dhparam"}},
		{"mta enables mta, amavis and sasl map", []string{"mta"}, []string{"amavis", "dhparam", "mta"}},
		{"proxy component enables proxy and nginx", []string{"proxy"}, []string{"dhparam", "nginx", "proxy"}},
		{"mailbox enables webxml", []string{"mailbox"}, []string{"dhparam", "webxml"}},
		{"cbpolicyd by its own name", []string{"cbpolicyd"}, []string{"cbpolicyd", "dhparam"}},
		{"dhparam service disables dhparam section", []string{"dhparam", "mta"}, []string{"amavis", "mta"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cm := parseCfForParity(t, cf, tt.enabled...)

			got := make([]string, 0, len(cm.State.MtaConfig.Sections))
			for name := range cm.State.MtaConfig.Sections {
				got = append(got, name)
			}

			slices.Sort(got)
			assert.Equal(t, tt.want, got)
		})
	}
}

// A disabled section must not resolve LDAP lookups nor leave comparator state.
func TestParseMtaConfig_DisabledSectionLeavesNoCompareState(t *testing.T) {
	t.Parallel()

	cm := parseCfForParity(t, `SECTION mta
	VAR zimbraMtaMyNetworks
SECTION proxy
	VAR zimbraReverseProxyHttpPort
`, "mta")

	cm.State.GlobalConfig.Data.Set("zimbraReverseProxyHttpPort", "8080")
	cm.State.GlobalConfig.Data.Set("zimbraMtaMyNetworks", "127.0.0.0/8")

	require.NoError(t, cm.CompareKeys(context.Background()))

	assert.NotContains(t, cm.State.LastVals, "proxy", "disabled section recorded last values")
	assert.Contains(t, cm.State.LastVals, "mta")
}

// legacy POSTCONF/POSTCONFD call section.requiredvars(fields[3], fields[2]) for a
// VAR/LOCAL/FILE (and, for POSTCONF, MAPLOCAL) value, so a change to the looked up
// key marks the section changed. Literal values register nothing.
func TestParseMtaConfig_PostconfValuesAreRequiredVars(t *testing.T) {
	t.Parallel()

	cm := parseCfForParity(t, `SECTION mta
	VAR explicitVar
	POSTCONF max_use VAR zimbraMtaMaxUse
	POSTCONF myhostname LOCAL zimbra_server_hostname
	POSTCONF sender_restrictions FILE zmconfigd/smtpd_sender_restrictions.cf
	POSTCONF dh_file MAPLOCAL zimbraSSLDHParam
	POSTCONF literal 0
	POSTCONF empty
	POSTCONFD del_var VAR zimbraDelVar
	POSTCONFD del_maplocal MAPLOCAL zimbraNotRegistered
	POSTCONFD del_plain
`, "mta")

	got := cm.State.MtaConfig.Sections["mta"].RequiredVars

	assert.Equal(t, map[string]string{
		"explicitVar":                            "VAR",
		"zimbraMtaMaxUse":                        "VAR",
		"zimbra_server_hostname":                 "LOCAL",
		"zmconfigd/smtpd_sender_restrictions.cf": "FILE",
		"zimbraSSLDHParam":                       "MAPLOCAL",
		"zimbraDelVar":                           "VAR",
	}, got)
}

// Legacy evaluates each if at load time and only a true branch registers the
// lookups of its POSTCONF lines. A false branch must stay untracked, or a change
// of its variable would mark the section changed and restart services for nothing.
func TestParseMtaConfig_ConditionalPostconfRequiredVarsOnlyWhenTrue(t *testing.T) {
	t.Parallel()

	const cf = `SECTION mta
	if VAR flagOn
		POSTCONF a VAR onVar
		POSTCONFD b
	fi
	if VAR flagOff
		POSTCONF c VAR offVar
	fi
	if VAR !flagOff
		POSTCONF d FILE zmconfigd/neg.cf
	fi
	if SERVICE cbpolicyd
		POSTCONF e LOCAL svcLocal
	fi
	if SERVICE !cbpolicyd
		POSTCONF f LOCAL notSvcLocal
	fi
	if VAR flagOn
		VAR plainOnVar
		LOCAL plainOnLocal
		POSTCONFD g LOCAL deleteLocal
	fi
	if VAR flagOff
		VAR plainOffVar
		LOCAL plainOffLocal
		POSTCONFD h LOCAL offDeleteLocal
	fi
`

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "zmconfigd.cf")
	require.NoError(t, os.WriteFile(cfgFile, []byte(cf), 0o644))

	cm := setupTestConfigManagerForParser(dir)
	cm.State.ServerConfig.ServiceConfig.Set("mta", "TRUE")
	cm.State.ServerConfig.ServiceConfig.Set("cbpolicyd", "TRUE")
	cm.State.GlobalConfig.Data.Set("flagOn", "TRUE")
	cm.State.GlobalConfig.Data.Set("flagOff", "FALSE")

	require.NoError(t, cm.ParseMtaConfig(context.Background(), cfgFile))

	assert.Equal(t, map[string]string{
		"onVar":            "VAR",
		"zmconfigd/neg.cf": "FILE",
		"svcLocal":         "LOCAL",
		"plainOnVar":       "VAR",
		"plainOnLocal":     "LOCAL",
		"deleteLocal":      "LOCAL",
	}, cm.State.MtaConfig.Sections["mta"].RequiredVars)
}

// Legacy ends an if block at the first fi and has no nesting: the inner header
// is ignored and its body belongs to the outer block, so its lookups count
// exactly when the OUTER condition holds, whatever the inner condition says.
func TestParseMtaConfig_NestedConditionalFollowsOuterCondition(t *testing.T) {
	t.Parallel()

	const cf = `SECTION mta
	if VAR flagOn
		POSTCONF g VAR outerVar
		if VAR flagOff
			POSTCONF h VAR innerVar
		fi
	fi
	if VAR flagOff
		POSTCONF i VAR offOuterVar
		if VAR flagOn
			POSTCONF j VAR offInnerVar
		fi
	fi
`

	cm := parseCfForParity(t, cf, "mta")
	cm.State.GlobalConfig.Data.Set("flagOn", "TRUE")
	cm.State.GlobalConfig.Data.Set("flagOff", "FALSE")

	section := cm.State.MtaConfig.Sections["mta"]
	require.Len(t, section.Conditionals, 2)

	got := map[string]string{}
	cm.registerConditionalRequiredVars(context.Background(), &config.MtaConfigSection{RequiredVars: got}, section.Conditionals)

	assert.Equal(t, map[string]string{"outerVar": "VAR", "innerVar": "VAR"}, got)
}
