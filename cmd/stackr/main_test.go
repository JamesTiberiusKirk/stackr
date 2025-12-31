package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

func TestParseArgsBasic(t *testing.T) {
	opts, help, version, err := parseArgs([]string{"myapp", "update"})
	require.NoError(t, err)
	require.False(t, help)
	require.False(t, version)
	require.Equal(t, stackcmd.Options{Stacks: []string{"myapp"}, Update: true}, opts)
}

func TestParseArgsVarsOnly(t *testing.T) {
	opts, help, version, err := parseArgs([]string{"myapp", "vars-only", "--", "env"})
	require.NoError(t, err)
	require.False(t, help)
	require.False(t, version)
	require.True(t, opts.VarsOnly)
	require.Equal(t, []string{"env"}, opts.VarsCommand)
}

func TestParseArgsUnknownFlag(t *testing.T) {
	_, _, _, err := parseArgs([]string{"--wat"})
	require.Error(t, err)
}

func TestParseArgsVersion(t *testing.T) {
	_, help, version, err := parseArgs([]string{"--version"})
	require.NoError(t, err)
	require.False(t, help)
	require.True(t, version)
}
