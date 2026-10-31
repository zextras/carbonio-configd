// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestComputeStringMD5(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"empty string", "", "d41d8cd98f00b204e9800998ecf8427e"},
		{"simple string", "hello world", "5eb63bbbe01eeed093cb22bb8f5acdc3"},
		{"multiline string", "line1\nline2\nline3", "81facad50c8e6244de64a98cf4f56f77"},
		{"config-like string", "mynetworks = 127.0.0.0/8, 10.0.0.0/8", "8e8652894d5c3b10b6a39d728f22d3b2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, ComputeStringMD5(tt.content))
		})
	}
}

func TestComputeFileMD5(t *testing.T) {
	t.Parallel()

	f := systest.New()
	testFile := filepath.Join("/etc/zextras", "test.conf")
	f.FS.Put(testFile, []byte("test configuration content\n"), 0o644)

	first, err := ComputeFileMD5(context.Background(), f.FS, testFile)
	require.NoError(t, err)
	assert.Len(t, first, 32)

	second, err := ComputeFileMD5(context.Background(), f.FS, testFile)
	require.NoError(t, err)
	assert.Equal(t, first, second, "identical content must hash identically")

	f.FS.Put(testFile, []byte("modified configuration content\n"), 0o644)

	third, err := ComputeFileMD5(context.Background(), f.FS, testFile)
	require.NoError(t, err)
	assert.NotEqual(t, first, third)
}

func TestComputeFileMD5_NonExistent(t *testing.T) {
	t.Parallel()

	f := systest.New()
	_, err := ComputeFileMD5(context.Background(), f.FS, "/nonexistent/file/path")
	assert.Error(t, err)
}

func TestState_FileMD5Cache(t *testing.T) {
	t.Parallel()

	s := NewState()
	path := "/opt/zextras/conf/postfix_main.cf"

	assert.Empty(t, s.GetFileMD5(path))

	s.SetFileMD5(path, "abc123def456")
	assert.Equal(t, "abc123def456", s.GetFileMD5(path))

	s.SetFileMD5(path, "789ghi012jkl")
	assert.Equal(t, "789ghi012jkl", s.GetFileMD5(path))
}

func TestState_FileHasChanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := systest.New()
	testFile := filepath.Join("/etc/zextras", "test.conf")
	f.FS.Put(testFile, []byte("initial content\n"), 0o644)

	s := NewState()

	assert.True(t, s.FileHasChanged(ctx, f.FS, testFile), "no cached MD5 means changed")

	require.NoError(t, s.UpdateFileMD5(ctx, f.FS, testFile))
	assert.False(t, s.FileHasChanged(ctx, f.FS, testFile))

	f.FS.Put(testFile, []byte("modified content\n"), 0o644)
	assert.True(t, s.FileHasChanged(ctx, f.FS, testFile))

	require.NoError(t, s.UpdateFileMD5(ctx, f.FS, testFile))
	assert.False(t, s.FileHasChanged(ctx, f.FS, testFile))
}

func TestState_FileHasChanged_NonExistent(t *testing.T) {
	t.Parallel()

	f := systest.New()
	s := NewState()

	assert.True(t, s.FileHasChanged(context.Background(), f.FS, "/nonexistent/file"))
}

func TestState_UpdateFileMD5_Error(t *testing.T) {
	t.Parallel()

	f := systest.New()
	s := NewState()

	assert.Error(t, s.UpdateFileMD5(context.Background(), f.FS, "/nonexistent/file"))
}

func TestState_ClearFileCache(t *testing.T) {
	t.Parallel()

	s := NewState()
	s.StoreFile("key1", "value1")
	s.StoreFile("key2", "value2")

	v, ok := s.LookupFile("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", v)

	s.ClearFileCache(context.Background())

	_, ok = s.LookupFile("key1")
	assert.False(t, ok)
	_, ok = s.LookupFile("key2")
	assert.False(t, ok)
}

func TestNewState(t *testing.T) {
	t.Parallel()

	s := NewState()

	assert.NotNil(t, s.ForcedConfig)
	assert.NotNil(t, s.RequestedConfig)
	assert.NotNil(t, s.FileCache)
	assert.NotNil(t, s.FileMD5Cache)
	assert.NotNil(t, s.WatchdogProcess)
	assert.NotNil(t, s.MtaConfig)
	assert.NotNil(t, s.MtaConfig.Sections)
	assert.True(t, s.IsFirstRun())
	assert.NotNil(t, s.Snapshot())
}

func TestState_SetForcedConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewState()

	assert.Empty(t, s.ForcedSections())

	s.SetForcedConfig(ctx, "mta")
	assert.Equal(t, "1", s.ForcedConfig["mta"])

	s.SetForcedConfig(ctx, "proxy")
	assert.Equal(t, []string{"mta", "proxy"}, s.ForcedSections())
}

func TestState_SetRequestedConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewState()

	s.SetRequestedConfig(ctx, "ldap")
	assert.Equal(t, "ldap", s.RequestedConfig["ldap"])

	s.SetRequestedConfig(ctx, "mailbox")
	assert.Len(t, s.RequestedConfig, 2)
}

func TestState_AddRequestedConfigs(t *testing.T) {
	t.Parallel()

	s := NewState()
	s.AddRequestedConfigs(context.Background(), []string{"mta", "proxy", "mta"})

	assert.Len(t, s.RequestedConfig, 2)
}

func TestState_ResetConfigs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewState()

	s.SetForcedConfig(ctx, "mta")
	s.SetForcedConfig(ctx, "proxy")
	s.SetRequestedConfig(ctx, "ldap")

	s.ResetForcedConfig(ctx)
	assert.Empty(t, s.ForcedSections())
	assert.Len(t, s.RequestedConfig, 1, "requested untouched by forced reset")

	s.ResetRequestedConfig(ctx)
	assert.Empty(t, s.RequestedConfig)
}

func TestState_SetSleepTimer(t *testing.T) {
	t.Parallel()

	s := NewState()

	assert.Zero(t, s.GetSleepTimer())

	s.SetSleepTimer(1.5)
	assert.InDelta(t, 1.5, s.GetSleepTimer(), 0)

	s.SetSleepTimer(0)
	assert.Zero(t, s.GetSleepTimer())
}

func TestState_SetFirstRun(t *testing.T) {
	t.Parallel()

	s := NewState()

	assert.True(t, s.IsFirstRun())

	s.SetFirstRun(false)
	assert.False(t, s.IsFirstRun())

	s.SetFirstRun(true)
	assert.True(t, s.IsFirstRun())
}

func TestState_GetSetDelWatchdog(t *testing.T) {
	t.Parallel()

	s := NewState()

	assert.Nil(t, s.GetWatchdog("antivirus"))

	s.SetWatchdog("antivirus", true)

	got := s.GetWatchdog("antivirus")
	require.NotNil(t, got)
	assert.True(t, *got)

	s.SetWatchdog("antivirus", false)

	got = s.GetWatchdog("antivirus")
	require.NotNil(t, got)
	assert.False(t, *got)

	s.DelWatchdog("antivirus")
	assert.Nil(t, s.GetWatchdog("antivirus"))

	assert.NotPanics(t, func() { s.DelWatchdog("nonexistent") })
}
