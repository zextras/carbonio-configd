// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"errors"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
)

func TestModifyOps_OrderIsDeletesAddsReplaces(t *testing.T) {
	t.Parallel()

	ops := modifyOps(
		map[string][]string{"b": {"1"}, "a": {"2", "3"}},
		map[string][]string{"z": {"9"}, "y": nil},
		map[string][]string{"r": {"x"}},
	)

	assert.Equal(t, []modifyOp{
		{modifyDelete, "y", nil},
		{modifyDelete, "z", []string{"9"}},
		{modifyAdd, "a", []string{"2", "3"}},
		{modifyAdd, "b", []string{"1"}},
		{modifyReplace, "r", []string{"x"}},
	}, ops)
}

func TestModifyOps_EmptyIsNoop(t *testing.T) {
	t.Parallel()

	assert.Empty(t, modifyOps(nil, nil, nil))

	// No request is sent for it: a nil client would panic on connect.
	assert.NoError(t, (&Client{}).ModifyValues("cn=config,cn=zimbra", nil, nil, nil))
}

func TestSingleValueOps_SplitsAddsAndDeletesOnly(t *testing.T) {
	t.Parallel()

	ops := []modifyOp{
		{modifyDelete, "d", []string{"1", "2"}},
		{modifyDelete, "whole", nil},
		{modifyAdd, "a", []string{"3", "4"}},
		{modifyReplace, "r", []string{"5", "6"}},
	}

	assert.Equal(t, []modifyOp{
		{modifyDelete, "d", []string{"1"}},
		{modifyDelete, "d", []string{"2"}},
		{modifyDelete, "whole", nil},
		{modifyAdd, "a", []string{"3"}},
		{modifyAdd, "a", []string{"4"}},
		{modifyReplace, "r", []string{"5", "6"}},
	}, singleValueOps(ops))
}

func TestIsAlreadyConverged(t *testing.T) {
	t.Parallel()

	assert.True(t, isAlreadyConverged(goldap.NewError(goldap.LDAPResultAttributeOrValueExists, errors.New("exists"))))
	assert.True(t, isAlreadyConverged(goldap.NewError(goldap.LDAPResultNoSuchAttribute, errors.New("absent"))))
	assert.False(t, isAlreadyConverged(goldap.NewError(goldap.LDAPResultInsufficientAccessRights, errors.New("denied"))))
	assert.False(t, isAlreadyConverged(nil))
}

func TestIsLDAPErrorRetryable_ConvergedResultsAreFinal(t *testing.T) {
	t.Parallel()

	assert.False(t, isLDAPErrorRetryable(goldap.NewError(goldap.LDAPResultAttributeOrValueExists, errors.New("exists"))))
	assert.False(t, isLDAPErrorRetryable(goldap.NewError(goldap.LDAPResultNoSuchAttribute, errors.New("absent"))))
}
