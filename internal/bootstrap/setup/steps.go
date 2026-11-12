// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

// Steps are the bootstrap steps in setup.pl order: the pre-steps of its main
// program, then applyConfig (L5829-6019).
func Steps() []Step {
	return []Step{
		StepPrepareHost(),

		// main
		StepInstallLdapConfig(),
		StepInstallLdapSchema(),
		StepAddCACert(),
		StepCheckCoreEnabled(),
		StepCheckPortConflicts(),
		StepZmldapupdateldif(),

		// applyConfig
		StepRemoveUnusedWebapps(),
		StepConfigLCMailboxd(),
		StepConfigLCValues(),
		StepConfigInitCore(),
		StepConfigCASetup(),
		StepConfigCreateCert(),
		StepConfigInstallCertStore(),
		StepConfigInstallCertMTA(),
		StepConfigInstallCertLDAP(),
		StepConfigInstallCertProxy(),
		StepConfigCreateServerEntryReplica(),
		StepUpdatePasswordsInLocalConfig(), // start of configSetupLdap
		StepConfigSetupLdap(),
		StepConfigCreateServerEntry(),
		StepConfigSaveCA(),
		StepConfigSaveCert(),
		StepGenerateDHParam(),
		StepConfigSetServicePorts(),
		StepConfigSetKeyboardShortcutsPref(),
		StepConfigSetStoreDefaults(),
		StepConfigSetStoreProxy(),
		StepConfigLDAPSchemaVersion(),
		StepConfigSetTimeZonePref(),
		StepSetSkinLogoURL(),
		StepConfigSetProxyPrefs(),
		StepSetProxyBits(),
		StepWarnDeprecatedVirtualIPs(),
		StepConfigInitMta(),
		StepConfigSetEnabledServices(),
		StepAddServerToHostPool(),
		StepSetTrainSACleanupHost(),
		StepConfigCreateDomain(),
		StepConfigInitSQL(),
		StepSetupSyslog(),
		StepStartLdap(),
		StepSetProxyPortsOnExisting(),
		StepStartServers(),
		StepConfigCreateDefaultDomainGALSyncAcct(),
		StepGALSyncSkipNotice(),
		StepSetupCrontab(),
		StepZmsshkeygen(),
		StepZmupdateauthkeys(),
	}
}
