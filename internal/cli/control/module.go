// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// moduleIDs maps the CLI name of a Carbonio Advanced module to its module id
// in the Advanced extension. "core" is deliberately absent, see rejectCore;
// "subscription" has no service switches.
var moduleIDs = map[string]string{
	"addressbook": "ZxAddressBook",
	"admin":       "ZxAdmin",
	"auth":        "ZxAuth",
	"backup":      "ZxBackup",
	"config":      "ZxConfig",
	"mailreplica": "ZxHA",
	"mobile":      "ZxMobile",
	"powerstore":  "ZxPowerstore",
}

// Modules returns the Advanced module names "configd control" can drive,
// sorted.
func Modules() []string {
	return slices.Sorted(maps.Keys(moduleIDs))
}

// serviceNameRe is the shape of a service-switch name.
var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// moduleVerbs maps the control actions to the Advanced service actions and the
// progressive form used in the output.
var moduleVerbs = map[Action]struct {
	action zxadmin.ServiceAction
	label  string
}{
	ActionStart:   {zxadmin.ServiceStart, "Starting"},
	ActionStop:    {zxadmin.ServiceStop, "Stopping"},
	ActionRestart: {zxadmin.ServiceRestart, "Restarting"},
}

// newModuleClient is the default orchestrator.newClient: a client for the
// local admin endpoint with the effective localconfig credentials.
func newModuleClient() (*zxadmin.Client, error) {
	cfg, err := localconfig.LoadEffective()
	if err != nil {
		return nil, fmt.Errorf("load localconfig: %w", err)
	}

	return zxadmin.New(cfg), nil
}

// validateModule rejects a request that cannot be sent: an unknown module, the
// core module (or one of its services) and a malformed service name.
func (o *orchestrator) validateModule(opts *Options) error {
	if opts.Module == "core" {
		return o.rejectCore()
	}

	if _, ok := moduleIDs[opts.Module]; !ok {
		return fmt.Errorf("unknown module %q (valid modules: %s)", opts.Module, strings.Join(Modules(), ", "))
	}

	if opts.Service != "" && !serviceNameRe.MatchString(opts.Service) {
		return fmt.Errorf("invalid service name %q", opts.Service)
	}

	return nil
}

// rejectCore explains why the core module cannot be controlled on its own:
// stopping or restarting it unregisters the Advanced HTTP handler of mailboxd,
// after which nothing can start it again but a mailbox restart.
func (o *orchestrator) rejectCore() error {
	restart := "zmmailboxdctl restart"
	if o.ctl.IsSystemdMode() {
		restart = "systemctl restart carbonio-appserver.service"
	}

	return fmt.Errorf("the core module and its services cannot be controlled separately: "+
		"restart the mailbox instead (%s)", restart)
}

// controlModule runs one start/stop/restart of an Advanced module, or of one
// of its services, through the running mailboxd.
func (o *orchestrator) controlModule(ctx context.Context, c *cliutil.Console, opts *Options) error {
	if err := o.validateModule(opts); err != nil {
		return err
	}

	if !o.ctl.AdvancedInstalled() {
		return errors.New("Carbonio Advanced is not installed") //nolint:staticcheck // user-facing sentence, product name
	}

	client, err := o.newClient()
	if err != nil {
		return err
	}

	service, what := zxadmin.ModuleService, opts.Module+" module"
	if opts.Service != "" && opts.Service != zxadmin.ModuleService {
		service, what = opts.Service, opts.Module+" "+opts.Service+" service"
	}

	verb := moduleVerbs[opts.Action]

	c.Header()
	c.Printf("\t%s %s...", verb.label, what)

	_, err = client.ControlService(ctx, moduleIDs[opts.Module], verb.action, service)
	if err != nil {
		c.Println("Failed.")

		if errors.Is(err, zxadmin.ErrAdvancedNotRunning) {
			return errors.New("Carbonio Advanced is not running") //nolint:staticcheck // user-facing sentence, product name
		}

		return err
	}

	c.Println("Done.")

	return nil
}
