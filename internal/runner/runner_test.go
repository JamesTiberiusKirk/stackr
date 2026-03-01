package runner

import (
	"testing"

	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
	"github.com/stretchr/testify/require"
)

func TestParseDeployArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want stackcmd.Options
	}{
		{
			name: "AllKnownArgs",
			args: []string{"update", "tear-down", "backup", "vars-only", "get-vars", "all"},
			want: stackcmd.Options{
				Update:   true,
				TearDown: true,
				Backup:   true,
				VarsOnly: true,
				GetVars:  true,
				All:      true,
			},
		},
		{
			name: "UnknownArgsIgnored",
			args: []string{"unknown", "random", "update"},
			want: stackcmd.Options{Update: true},
		},
		{
			name: "EmptyArgs",
			args: []string{},
			want: stackcmd.Options{},
		},
		{
			name: "NilArgs",
			args: nil,
			want: stackcmd.Options{},
		},
		{
			name: "MultipleArgs",
			args: []string{"testapp", "update"},
			want: stackcmd.Options{Update: true},
		},
		{
			name: "UpdateOnly",
			args: []string{"update"},
			want: stackcmd.Options{Update: true},
		},
		{
			name: "TearDownOnly",
			args: []string{"tear-down"},
			want: stackcmd.Options{TearDown: true},
		},
		{
			name: "BackupOnly",
			args: []string{"backup"},
			want: stackcmd.Options{Backup: true},
		},
		{
			name: "VarsOnlyOnly",
			args: []string{"vars-only"},
			want: stackcmd.Options{VarsOnly: true},
		},
		{
			name: "GetVarsOnly",
			args: []string{"get-vars"},
			want: stackcmd.Options{GetVars: true},
		},
		{
			name: "AllOnly",
			args: []string{"all"},
			want: stackcmd.Options{All: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDeployArgs(tt.args)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCommandErrorDoesNotLeakOutput(t *testing.T) {
	t.Run("empty stdout and stderr", func(t *testing.T) {
		cmdErr := &CommandError{
			Msg:  "deployment failed for stack=myapp",
			Code: 1,
		}

		require.Equal(t, "", cmdErr.Stdout, "CommandError should not contain stdout")
		require.Equal(t, "", cmdErr.Stderr, "CommandError should not contain stderr")
		require.Equal(t, "deployment failed for stack=myapp", cmdErr.Error())
		require.Equal(t, 1, cmdErr.Code)
	})

	t.Run("error interface works", func(t *testing.T) {
		var err error = &CommandError{
			Msg:  "test error",
			Code: 42,
		}
		require.EqualError(t, err, "test error")
	})
}
