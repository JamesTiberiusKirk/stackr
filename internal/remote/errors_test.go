package remote

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCloneError(t *testing.T) {
	t.Helper()

	// Test generic clone error
	err := NewCloneError("myapp", "git@github.com:org/repo.git", errors.New("some error"))
	require.Error(t, err)

	errMsg := err.Error()
	require.Contains(t, errMsg, "myapp")
	require.Contains(t, errMsg, "git clone")
	require.Contains(t, errMsg, "Hint:")
	require.Contains(t, errMsg, "SSH access")
}

func TestNewCloneError_PermissionDenied(t *testing.T) {
	t.Helper()

	// Test SSH permission denied error
	err := NewCloneError("myapp", "git@github.com:org/repo.git", errors.New("Permission denied (publickey)"))
	require.Error(t, err)

	errMsg := err.Error()
	require.Contains(t, errMsg, "SSH permission denied")
	require.Contains(t, errMsg, "ssh-add -l")
	require.Contains(t, errMsg, "git ls-remote")
}

func TestNewCloneError_NetworkError(t *testing.T) {
	t.Helper()

	// Test network error
	err := NewCloneError("myapp", "git@github.com:org/repo.git", errors.New("Could not resolve hostname github.com"))
	require.Error(t, err)

	errMsg := err.Error()
	require.Contains(t, errMsg, "Network error")
	require.Contains(t, errMsg, "internet connection")
	require.Contains(t, errMsg, "DNS is working")
}

func TestNewCheckoutError(t *testing.T) {
	t.Helper()

	// Test tag checkout error
	err := NewCheckoutError("myapp", "v1.2.3", "tag", errors.New("pathspec 'v1.2.3' did not match"))
	require.Error(t, err)

	errMsg := err.Error()
	require.Contains(t, errMsg, "myapp")
	require.Contains(t, errMsg, "git checkout")
	require.Contains(t, errMsg, "v1.2.3")
	require.Contains(t, errMsg, "tag")
	require.Contains(t, errMsg, "does not exist")
	require.Contains(t, errMsg, "git ls-remote --tags")
}

func TestNewVersionRefError(t *testing.T) {
	t.Helper()

	err := NewVersionRefError("myapp", "${APP_VERSION}", "APP_VERSION")
	require.Error(t, err)

	errMsg := err.Error()
	require.Contains(t, errMsg, "myapp")
	require.Contains(t, errMsg, "resolve version ref")
	require.Contains(t, errMsg, "APP_VERSION")
	require.Contains(t, errMsg, ".env file")
	require.Contains(t, errMsg, "Hint:")
}

func TestNewPullError(t *testing.T) {
	t.Helper()

	err := NewPullError("myapp", errors.New("network timeout"))
	require.Error(t, err)

	errMsg := err.Error()
	require.Contains(t, errMsg, "myapp")
	require.Contains(t, errMsg, "git pull")
	require.Contains(t, errMsg, "not critical")
	require.Contains(t, errMsg, "cached version")
}

func TestStackError_Unwrap(t *testing.T) {
	t.Helper()

	causeErr := errors.New("underlying error")
	stackErr := &StackError{
		StackName: "test",
		Operation: "test-op",
		Cause:     causeErr,
	}

	require.Equal(t, causeErr, errors.Unwrap(stackErr))
}

func TestStackError_ErrorFormatting(t *testing.T) {
	t.Helper()

	// Test with cause and hint
	err := &StackError{
		StackName: "myapp",
		Operation: "deploy",
		Cause:     errors.New("deployment failed"),
		Hint:      "Check your configuration",
	}

	errMsg := err.Error()
	require.Contains(t, errMsg, "remote stack 'myapp'")
	require.Contains(t, errMsg, "deploy failed")
	require.Contains(t, errMsg, "deployment failed")
	require.Contains(t, errMsg, "Hint: Check your configuration")

	// Test without hint
	err2 := &StackError{
		StackName: "myapp",
		Operation: "deploy",
		Cause:     errors.New("deployment failed"),
	}

	errMsg2 := err2.Error()
	require.Contains(t, errMsg2, "remote stack 'myapp'")
	require.NotContains(t, errMsg2, "Hint:")

	// Test without cause
	err3 := &StackError{
		StackName: "myapp",
		Operation: "deploy",
	}

	errMsg3 := err3.Error()
	require.Contains(t, errMsg3, "remote stack 'myapp'")
	require.Contains(t, errMsg3, "deploy failed")
}

func TestErrorMessages_AreHelpful(t *testing.T) {
	t.Helper()

	testCases := []struct {
		name         string
		err          error
		mustContain  []string
		mustNotEmpty bool
	}{
		{
			name: "clone error has actionable steps",
			err:  NewCloneError("test", "git@github.com:test/repo.git", errors.New("error")),
			mustContain: []string{
				"Check that:",
				"repository URL",
				"SSH access",
			},
			mustNotEmpty: true,
		},
		{
			name: "checkout error lists available actions",
			err:  NewCheckoutError("test", "v1.0", "tag", errors.New("error")),
			mustContain: []string{
				"does not exist",
				"tag/commit exists",
				"version in your .env",
			},
			mustNotEmpty: true,
		},
		{
			name: "version ref error explains solution",
			err:  NewVersionRefError("test", "${VAR}", "VAR"),
			mustContain: []string{
				"environment variable",
				".env file",
				"To fix this:",
			},
			mustNotEmpty: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := tc.err.Error()

			if tc.mustNotEmpty {
				require.NotEmpty(t, errMsg)
			}

			for _, substr := range tc.mustContain {
				require.Contains(t, errMsg, substr,
					"Error message should contain '%s'\nFull error: %s", substr, errMsg)
			}

			// Error messages should have reasonable length (not too short, not too long)
			require.Greater(t, len(errMsg), 50, "Error message too short")
			require.Less(t, len(errMsg), 1000, "Error message too long")

			// Error messages should not have trailing/leading whitespace on lines
			lines := strings.Split(errMsg, "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					// Allow indentation, but not trailing spaces
					require.NotContains(t, line, "  \n",
						"Line %d has trailing whitespace: %q", i, line)
				}
			}
		})
	}
}
