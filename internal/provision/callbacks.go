// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import "slices"

// entryKind is the kind of entry a modification applies to.
type entryKind int

const (
	// kindNone is an entry being created.
	kindNone entryKind = iota
	kindConfig
	kindServer
	kindCOS
)

// callbackContext is the CallbackContext of the Java code plus the entry the
// callbacks look at: nil while the entry is being created.
type callbackContext struct {
	p     *Provisioner
	kind  entryKind
	entry *Entry
	// create is set for an entry being created.
	create bool
	done   map[string]bool
}

// once is CallbackContext.isDoneAndSetIfNot: it reports whether the callback
// has run already in this operation and marks it as run.
func (c *callbackContext) once(callback string) bool {
	was := c.done[callback]
	c.done[callback] = true

	return was
}

// attrCallback is an attribute callback of attrs.xml (AttributeCallback).
type attrCallback struct {
	// pre runs before the modification, may change mods and refuses the request
	// with an error. values are those of the key being checked.
	pre func(c *callbackContext, attr string, values []string, mods Mods) error
	// post runs after the modification on the entry as it is then; its failure
	// is only a warning.
	post func(c *callbackContext, attr string) error
}

// callbacks are the callbacks the native provisioning ports, by the name in
// attrs.xml. The others are refused (see preModify): the run would otherwise
// change an attribute without what the Java code does around it.
//
// Callbacks that do nothing for the entries the commands touch (servers, the
// global config, classes of service, and entries being created) are listed
// with an empty callback: ServerConfig reloads a running mailbox (it checks
// Zimbra.started()), DisplayName and AccountStatus's post only apply to an
// existing account, CosId validates ids only for a non-UUID provisioning,
// MtaAuthHost does nothing.
var callbacks = map[string]attrCallback{
	"CheckPortConflict":    {pre: checkPortConflict},
	"MailPort":             {pre: checkPortConflict, post: mailPortPost},
	"MailHostPool":         {pre: mailHostPoolPre},
	"MailHost":             {pre: mailHostPre},
	"LocalBind":            {post: localBindPost},
	"AccountStatus":        {pre: accountStatusPre},
	"DomainStatus":         {pre: domainStatusPre},
	"MtaRelayHost":         {pre: mtaRelayHostPre},
	"PositiveTimeInterval": {pre: positiveIntervalPre},
	"ServerConfig":         {},
	"DisplayName":          {},
	"CosId":                {},
	"MtaAuthHost":          {},
}

// preModify is LdapAttributeCallbackHelper.preModify with checkImmutable: the
// value check of every key of mods and its attribute callback. The keys are
// those at the start; a callback that adds keys does not get them checked.
func (p *Provisioner) preModify(c *callbackContext, mods Mods) error {
	for _, key := range mods.keys() {
		_, name := split(key)
		if name == "" {
			return fail(CodeInvalidAttrName, "empty attr name found")
		}

		a := p.Schema.Lookup(name)
		if a == nil {
			p.Warn("checkValue: no attribute info for: %s", name)

			continue
		}

		for _, v := range mods[key] {
			if err := p.Schema.Check(name, v, true); err != nil {
				return err
			}
		}

		if err := p.runPre(c, a, mods, key); err != nil {
			return err
		}
	}

	return nil
}

func (p *Provisioner) runPre(c *callbackContext, a *Attr, mods Mods, key string) error {
	if a.Callback == "" {
		return nil
	}

	cb, ok := callbacks[a.Callback]
	if !ok {
		return invalidRequest("%s is checked by the %s callback, which the native provisioning does not implement",
			a.Name, a.Callback)
	}

	if cb.pre == nil {
		return nil
	}

	return cb.pre(c, a.Name, slices.Clone(mods[key]), mods)
}

// postModify is LdapAttributeCallbackHelper.postModify: the post callbacks of
// the keys of mods; a failure is a warning.
func (p *Provisioner) postModify(c *callbackContext, mods Mods) {
	for _, key := range mods.keys() {
		_, name := split(key)

		a := p.Schema.Lookup(name)
		if a == nil || a.Callback == "" {
			continue
		}

		cb, ok := callbacks[a.Callback]
		if !ok || cb.post == nil {
			continue
		}

		if err := cb.post(c, a.Name); err != nil {
			p.Warn("postModify of %s: %v", a.Name, err)
		}
	}
}

// valuesToBe is CallbackUtil.newValuesToBe: the values an attribute has after
// the modification.
func valuesToBe(entry *Entry, name string, kind multiKind, values []string) []string {
	current := entry.Values(name)

	switch kind {
	case multiReplacing:
		return values
	case multiAdding:
		out := slices.Clone(current)

		for _, v := range values {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}

		return out
	case multiRemoving:
		return slices.DeleteFunc(slices.Clone(current), func(v string) bool { return slices.Contains(values, v) })
	case multiDeleting:
		return nil
	default:
		return current
	}
}

// accountStatusPre is the preModify of AccountStatus: the mail status follows
// the account status.
func accountStatusPre(_ *callbackContext, attr string, _ []string, mods Mods) error {
	status, set, err := mods.singleValue(attr)
	if err != nil {
		return err
	}

	if !set {
		return invalidRequest("%s is a required attribute", attr)
	}

	switch {
	case status == "closed" || status == "pending":
		mods[attrMailStatus] = []string{statusDisabled}
	case len(mods[attrMailStatus]) == 0:
		mods[attrMailStatus] = []string{"enabled"}
	}

	return nil
}

// domainStatusPre is the preModify of DomainStatus.
func domainStatusPre(c *callbackContext, attr string, values []string, mods Mods) error {
	if len(values) != 1 {
		return invalidRequest("%s is a single-valued attribute", attr)
	}

	switch status := values[0]; status {
	case "shutdown":
		return invalidRequest("Setting %s to shutdown is not allowed.  "+
			"It is an internal status and can only be set by server", attr)
	case "closed":
		mods[attrMailStatus] = []string{statusDisabled}
	default:
		if len(mods[attrMailStatus]) > 0 {
			return nil
		}

		if c.entry != nil && status == "suspended" && c.entry.First(attrMailStatus) == statusDisabled {
			return nil
		}

		mods[attrMailStatus] = []string{"enabled"}
	}

	return nil
}

// mtaRelayHostPre is the preModify of MtaRelayHost: one value at most.
func mtaRelayHostPre(c *callbackContext, attr string, _ []string, mods Mods) error {
	kind, values := mods.multiValue(attr)
	if len(valuesToBe(c.entry, attr, kind, values)) > 1 {
		return invalidRequest("%s can only have one value", attr)
	}

	return nil
}

// positiveIntervalPre is the preModify of PositiveTimeInterval.
func positiveIntervalPre(_ *callbackContext, attr string, _ []string, mods Mods) error {
	value, set, err := mods.singleValue(attr)
	if err != nil || !set {
		return err
	}

	if d, perr := parseDuration(value); perr != nil || d <= 0 {
		return invalidRequest("cannot set %s less than or equal to 0", attr)
	}

	return nil
}
