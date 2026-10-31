// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package logger

import (
	"context"
	"log/slog"

	"github.com/zextras/carbonio-configd/internal/redact"
)

// redactHandler wraps an slog.Handler and masks secrets in every record before
// the wrapped handler sees it, so no call site can leak one:
//
//   - an attribute whose key names a secret (password, token, privatekey, ...,
//     see redact.IsSecretKey) has its value replaced by redact.Placeholder;
//   - a value attribute ("value", "previous_value", ...) logged next to a key
//     attribute ("key", "attr", ...) that names a secret is replaced too;
//   - PEM private key blocks inside any string attribute, error attribute or
//     the message are replaced by redact.Placeholder.
//
// Records that carry no secret are forwarded untouched, so ordinary log output
// stays byte-identical. Empty values are kept as-is (an unset key is visible).
type redactHandler struct {
	inner slog.Handler
}

func newRedactHandler(inner slog.Handler) slog.Handler {
	return &redactHandler{inner: inner}
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

//nolint:gocritic // slog.Handler requires the record by value
func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	msg, changed := redact.Text(r.Message)

	var attrs []slog.Attr

	if r.NumAttrs() > 0 {
		all := make([]slog.Attr, 0, r.NumAttrs())

		r.Attrs(func(a slog.Attr) bool {
			all = append(all, a)

			return true
		})

		var attrsChanged bool

		attrs, attrsChanged = redactAttrs(all)
		changed = changed || attrsChanged
	}

	if !changed {
		return h.inner.Handle(ctx, r)
	}

	out := slog.NewRecord(r.Time, r.Level, msg, r.PC)
	out.AddAttrs(attrs...)

	return h.inner.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted, _ := redactAttrs(attrs)

	return &redactHandler{inner: h.inner.WithAttrs(redacted)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name)}
}

// redactAttrs returns attrs with secrets masked and whether anything changed.
// The input slice is never modified; on no change it is returned as-is.
func redactAttrs(attrs []slog.Attr) ([]slog.Attr, bool) {
	secretKeyed := false

	for _, a := range attrs {
		if redact.IsKeyAttr(a.Key) && a.Value.Kind() == slog.KindString && redact.IsSecretKey(a.Value.String()) {
			secretKeyed = true

			break
		}
	}

	var out []slog.Attr

	for i, a := range attrs {
		masked, changed := redactAttr(a, secretKeyed && redact.IsValueAttr(a.Key))
		if !changed {
			continue
		}

		if out == nil {
			out = make([]slog.Attr, len(attrs))
			copy(out, attrs)
		}

		out[i] = masked
	}

	if out == nil {
		return attrs, false
	}

	return out, true
}

// redactAttr masks one attribute. forced is true when a sibling key attribute
// names a secret and this attribute is therefore its value.
func redactAttr(a slog.Attr, forced bool) (slog.Attr, bool) {
	v := a.Value.Resolve()

	if v.Kind() == slog.KindGroup {
		group, changed := redactAttrs(v.Group())
		if !changed {
			return a, false
		}

		return slog.Attr{Key: a.Key, Value: slog.GroupValue(group...)}, true
	}

	secret := forced || redact.IsSecretKey(a.Key)

	switch v.Kind() {
	case slog.KindString:
		return redactString(a, v.String(), secret)
	case slog.KindAny:
		return redactAny(a, v, secret)
	default:
		// Numbers, bools, durations and times cannot carry a credential.
	}

	return a, false
}

// redactString masks a string attribute: wholly when secret, else any
// credential embedded in its text.
func redactString(a slog.Attr, s string, secret bool) (slog.Attr, bool) {
	if s == "" {
		return a, false
	}

	if secret {
		return slog.String(a.Key, redact.Placeholder), true
	}

	if text, ok := redact.Text(s); ok {
		return slog.String(a.Key, text), true
	}

	return a, false
}

// redactAny masks an any-valued attribute: wholly when secret, else the text
// of an error value.
func redactAny(a slog.Attr, v slog.Value, secret bool) (slog.Attr, bool) {
	if secret && v.Any() != nil {
		return slog.String(a.Key, redact.Placeholder), true
	}

	if err, ok := v.Any().(error); ok && err != nil {
		if text, changed := redact.Text(err.Error()); changed {
			return slog.String(a.Key, text), true
		}
	}

	return a, false
}
