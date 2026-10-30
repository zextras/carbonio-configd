// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/zextras/carbonio-configd/internal/redact"
)

// Redacted returns a deep copy of p in which the values of secret keys are
// replaced by the redaction placeholder. Rendering always goes through it, so
// neither the text nor the JSON output can leak a literal secret.
func (p *Plan) Redacted() Plan {
	out := *p
	out.Postconf = slices.Clone(p.Postconf)
	out.Postconfd = slices.Clone(p.Postconfd)
	out.Ldap = slices.Clone(p.Ldap)

	for i := range out.Postconf {
		out.Postconf[i].Value = redact.ForLog(out.Postconf[i].Key, out.Postconf[i].Value)
	}

	for i := range out.Postconfd {
		out.Postconfd[i].Value = redact.ForLog(out.Postconfd[i].Key, out.Postconfd[i].Value)
	}

	for i := range out.Ldap {
		out.Ldap[i].Value = redact.ForLog(out.Ldap[i].Key, out.Ldap[i].Value)
	}

	return out
}

// JSON renders the plan as indented JSON with a trailing newline. The output
// is deterministic: equal plans give identical bytes.
func (p *Plan) JSON() ([]byte, error) {
	red := p.Redacted()

	data, err := json.MarshalIndent(&red, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}

	return append(data, '\n'), nil
}

// WriteText writes the plan for humans: one line per action followed by its
// reasons, then the predicted cascade.
func (p *Plan) WriteText(w io.Writer) error {
	red := p.Redacted()
	tw := &textWriter{w: w}

	if red.IsEmpty() && len(red.Heal) == 0 {
		tw.printf("No changes.\n")

		return tw.err
	}

	for i := range red.Mapfiles {
		m := &red.Mapfiles[i]
		tw.item("mapfile", m.Key, m.Reasons)
	}

	for i := range red.Heal {
		m := &red.Heal[i]
		tw.item("mapfile-sync", m.Key, m.Reasons)
	}

	for i := range red.Rewrites {
		r := &red.Rewrites[i]
		tw.item("rewrite", r.Source+" -> "+r.Target, r.Reasons)
	}

	for i := range red.Postconf {
		c := &red.Postconf[i]
		tw.item("postconf -e", c.Key+"="+c.Value, c.Reasons)
	}

	for i := range red.Postconfd {
		c := &red.Postconfd[i]
		tw.item("postconf -X", c.Key, c.Reasons)
	}

	for i := range red.Ldap {
		l := &red.Ldap[i]
		tw.item("ldap", l.Key+"="+l.Value, l.Reasons)
	}

	if red.Proxygen != nil {
		tw.item("proxygen", "regenerate proxy configuration", red.Proxygen.Reasons)
	}

	for i := range red.Restarts {
		r := &red.Restarts[i]
		tw.item(string(r.Action), r.Service, r.Reasons)
	}

	for i := range red.Cascade {
		c := &red.Cascade[i]
		tw.item("restart", c.Service+" (predicted cascade)", c.Reasons)
	}

	return tw.err
}

type textWriter struct {
	w   io.Writer
	err error
}

func (t *textWriter) printf(format string, args ...any) {
	if t.err != nil {
		return
	}

	_, t.err = fmt.Fprintf(t.w, format, args...)
}

func (t *textWriter) item(kind, subject string, reasons []Reason) {
	t.printf("%-12s %s\n", kind, subject)

	for i := range reasons {
		t.printf("    because: %s\n", reasons[i].String())
	}
}
