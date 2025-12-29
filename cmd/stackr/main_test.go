package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

func TestParseArgsBasic(t *testing.T) {
	opts, help, err := parseArgs([]string{"mx5parts", "update"})
	require.NoError(t, err)
	require.False(t, help)
	require.Equal(t, stackcmd.Options{Stacks: []string{"mx5parts"}, Update: true}, opts)
}

func TestParseArgsVarsOnly(t *testing.T) {
	opts, help, err := parseArgs([]string{"immich", "vars-only", "--", "env"})
	require.NoError(t, err)
	require.False(t, help)
	require.True(t, opts.VarsOnly)
	require.Equal(t, []string{"env"}, opts.VarsCommand)
}

func TestParseArgsUnknownFlag(t *testing.T) {
	_, _, err := parseArgs([]string{"--wat"})
	require.Error(t, err)
}
