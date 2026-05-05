package dockerwatch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectFromName(t *testing.T) {
	tests := []struct {
		name        string
		composeName string
		dirName     string
		want        string
	}{
		{
			name:        "explicit name wins over dir",
			composeName: "stackr-sandbox-minimal",
			dirName:     "stackr",
			want:        "stackr-sandbox-minimal",
		},
		{
			name:        "no name falls back to dir",
			composeName: "",
			dirName:     "nginx",
			want:        "nginx",
		},
		{
			name:        "dir name gets lowercased and stripped",
			composeName: "",
			dirName:     "My_Stack-01",
			want:        "my_stack-01",
		},
		{
			name:        "whitespace in compose name trimmed",
			composeName: "  proj  ",
			dirName:     "anything",
			want:        "proj",
		},
		{
			name:        "spaces in dir collapse — compose normalisation",
			composeName: "",
			dirName:     "weird name with spaces",
			want:        "weirdnamewithspaces",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, projectFromName(tt.composeName, tt.dirName))
		})
	}
}

func TestNextBackoff(t *testing.T) {
	require.Equal(t, 2*reconnectBackoffMax/2, nextBackoff(reconnectBackoffMax/2),
		"sanity check: doubling stays under the cap until it overflows")

	// At the cap or above, must clamp.
	require.Equal(t, reconnectBackoffMax, nextBackoff(reconnectBackoffMax),
		"backoff must clamp at the cap so a long docker outage doesn't stretch reconnect to hours")
	require.Equal(t, reconnectBackoffMax, nextBackoff(reconnectBackoffMax*2),
		"backoff must clamp even when callers pass already-capped values")
}
