package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseComposePs_JSONLines(t *testing.T) {
	// Newer docker compose emits one JSON object per line. The parser
	// must accept that without choking on the missing top-level array.
	input := []byte(`{"Name":"nginx-web-1","Service":"web","State":"running","Status":"Up 2 minutes","Image":"nginx:alpine"}
{"Name":"nginx-bg-1","Service":"bg","State":"exited","Status":"Exited (0) 1 minute ago","Image":"alpine"}
`)
	got, err := parseComposePs(input)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "web", got[0].Service)
	require.Equal(t, "running", got[0].State)
	require.Equal(t, "exited", got[1].State)
}

func TestParseComposePs_JSONArray(t *testing.T) {
	// Older compose emits a single JSON array — parser auto-detects via
	// the leading '['.
	input := []byte(`[
		{"Name":"nginx-web-1","Service":"web","State":"running","Status":"Up","Image":"nginx:alpine"}
	]`)
	got, err := parseComposePs(input)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "web", got[0].Service)
}

func TestParseComposePs_Empty(t *testing.T) {
	got, err := parseComposePs([]byte{})
	require.NoError(t, err)
	require.Empty(t, got, "no output is a valid empty result — a never-brought-up stack hits this branch")
}

func TestStackStatusAggregate(t *testing.T) {
	tests := []struct {
		name     string
		services []ServiceStatus
		want     string
	}{
		{
			name: "no services declared",
			want: StackAggregateEmpty,
		},
		{
			name: "all running",
			services: []ServiceStatus{
				{State: ServiceStateRunning},
				{State: ServiceStateRunning},
			},
			want: StackAggregateRunning,
		},
		{
			name: "none running — all stopped",
			services: []ServiceStatus{
				{State: ServiceStateExited},
				{State: ServiceStateMissing},
			},
			want: StackAggregateStopped,
		},
		{
			name: "some running — partial",
			services: []ServiceStatus{
				{State: ServiceStateRunning},
				{State: ServiceStateExited},
			},
			want: StackAggregatePartial,
		},
		{
			name: "restarting doesn't count as running for the aggregate",
			services: []ServiceStatus{
				{State: ServiceStateRunning},
				{State: ServiceStateRestarting},
			},
			want: StackAggregatePartial,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := StackStatus{Services: tt.services}
			require.Equal(t, tt.want, ss.Aggregate())
		})
	}
}

func TestRunningCount(t *testing.T) {
	ss := StackStatus{Services: []ServiceStatus{
		{State: ServiceStateRunning},
		{State: ServiceStateRunning},
		{State: ServiceStateExited},
	}}
	require.Equal(t, 2, ss.RunningCount())
	require.Equal(t, 3, ss.TotalCount())
}
