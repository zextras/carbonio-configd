// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"regexp"
	"strconv"
	"unicode"
	"unicode/utf16"
)

// The password policy attributes of an account or its class of service.
const (
	attrPwdMinLength    = "zimbraPasswordMinLength"
	attrPwdMaxLength    = "zimbraPasswordMaxLength"
	attrPwdMinUpper     = "zimbraPasswordMinUpperCaseChars"
	attrPwdMinLower     = "zimbraPasswordMinLowerCaseChars"
	attrPwdMinNumeric   = "zimbraPasswordMinNumericChars"
	attrPwdMinPunct     = "zimbraPasswordMinPunctuationChars"
	attrPwdMinAlpha     = "zimbraPasswordMinAlphaChars"
	attrPwdMinDigitPunc = "zimbraPasswordMinDigitsOrPuncs"
	attrPwdAllowed      = "zimbraPasswordAllowedChars"
	attrPwdAllowedPunct = "zimbraPasswordAllowedPunctuationChars"
)

// policy is the password policy that applies to an account being created: the
// values of the account's own attributes, then those of its class of service.
type policy struct {
	cos   *Entry
	attrs attrSet
}

// value is the value of the attribute for the account: given at creation, else
// from the class of service, "" for none.
func (pl policy) value(name string) string {
	if v := pl.attrs.first(name); v != "" {
		return v
	}

	return pl.cos.First(name)
}

// num is the integer value of the attribute, 0 when absent or not a number.
func (pl policy) num(name string) int {
	n, err := strconv.Atoi(pl.value(name))
	if err != nil {
		return 0
	}

	return n
}

// pattern compiles the regex of an attribute as a whole-character match; nil
// when the attribute has no value.
func (pl policy) pattern(name string) (*regexp.Regexp, error) {
	v := pl.value(name)
	if v == "" {
		return nil, nil //nolint:nilnil // no value, no pattern
	}

	re, err := regexp.Compile("^(?:" + v + ")$")
	if err != nil {
		return nil, fail(CodeBadPassword, "%s is not valid regex: %v", name, err)
	}

	return re, nil
}

// counts is how many characters of each class a password has.
type counts struct{ upper, lower, numeric, punct, alpha int }

// checkPasswordStrength is LdapProvisioning.checkPasswordStrength for an
// account being created: the policy comes from the attributes it is created
// with and its class of service. The checks that need an existing account (the
// common password filter and the personal data check) do not apply.
func checkPasswordStrength(password string, pl policy) error {
	units := utf16.Encode([]rune(password))

	if n := pl.num(attrPwdMinLength); n > 0 && len(units) < n {
		return fail(CodeBadPassword, "too short (%s=%d)", attrPwdMinLength, n)
	}

	if n := pl.num(attrPwdMaxLength); n > 0 && len(units) > n {
		return fail(CodeBadPassword, "too long (%s=%d)", attrPwdMaxLength, n)
	}

	allowed, err := pl.pattern(attrPwdAllowed)
	if err != nil {
		return err
	}

	allowedPunct, err := pl.pattern(attrPwdAllowedPunct)
	if err != nil {
		return err
	}

	c, err := classify(units, allowed, allowedPunct)
	if err != nil {
		return err
	}

	return c.check(pl)
}

// classify counts the characters of the password by class.
func classify(units []uint16, allowed, allowedPunct *regexp.Regexp) (counts, error) {
	var c counts

	for _, u := range units {
		ch := rune(u)
		if allowed != nil && !allowed.MatchString(string(ch)) {
			return c, fail(CodeBadPassword, "%c is not an allowed character (%s)", ch, attrPwdAllowed)
		}

		isAlpha := true

		switch {
		case unicode.IsUpper(ch):
			c.upper++
		case unicode.IsLower(ch):
			c.lower++
		case unicode.IsDigit(ch):
			c.numeric++
			isAlpha = false
		case allowedPunct != nil:
			if allowedPunct.MatchString(string(ch)) {
				c.punct++
				isAlpha = false
			}
		case isASCIIPunct(ch):
			c.punct++
			isAlpha = false
		}

		if isAlpha {
			c.alpha++
		}
	}

	return c, nil
}

// check compares the counts with the minimums of the policy; a policy with no
// minimum accepts any password.
func (c counts) check(pl policy) error {
	minimums := []struct {
		attr    string
		message string
		have    int
	}{
		{attrPwdMinUpper, "not enough upper case characters", c.upper},
		{attrPwdMinLower, "not enough lower case characters", c.lower},
		{attrPwdMinNumeric, "not enough numeric characters", c.numeric},
		{attrPwdMinPunct, "not enough punctuation characters", c.punct},
		{attrPwdMinAlpha, "not enough alpha characters", c.alpha},
		{attrPwdMinDigitPunc, "not enough numeric or punctuation characters", c.numeric + c.punct},
	}

	for _, m := range minimums {
		if need := pl.num(m.attr); m.have < need {
			return fail(CodeBadPassword, "%s (%s=%d)", m.message, m.attr, need)
		}
	}

	return nil
}

// isASCIIPunct is LdapProvisioning.isAsciiPunc.
func isASCIIPunct(ch rune) bool {
	return (ch >= 33 && ch <= 47) || (ch >= 58 && ch <= 64) || (ch >= 91 && ch <= 96) || (ch >= 123 && ch <= 126)
}
