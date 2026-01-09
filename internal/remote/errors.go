package remote

import (
	"fmt"
	"strings"
)

// StackError represents an error specific to remote stack operations
type StackError struct {
	StackName string
	Operation string
	Cause     error
	Hint      string
}

func (e *StackError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "remote stack '%s': %s failed", e.StackName, e.Operation)

	if e.Cause != nil {
		fmt.Fprintf(&b, ": %v", e.Cause)
	}

	if e.Hint != "" {
		fmt.Fprintf(&b, "\n\nHint: %s", e.Hint)
	}

	return b.String()
}

func (e *StackError) Unwrap() error {
	return e.Cause
}

// NewCloneError creates an error for git clone failures
func NewCloneError(stackName, url string, cause error) error {
	hint := "Check that:\n" +
		"  1. The repository URL is correct\n" +
		"  2. You have SSH access to the repository (try: ssh -T git@github.com)\n" +
		"  3. Your SSH keys are properly configured\n" +
		"  4. The branch specified in stackr.yaml exists"

	if strings.Contains(cause.Error(), "Permission denied") {
		hint = "SSH permission denied. Ensure:\n" +
			"  1. Your SSH key is added to your SSH agent (try: ssh-add -l)\n" +
			"  2. Your public key is added to the remote Git service\n" +
			fmt.Sprintf("  3. You can access the repo (try: git ls-remote %s)", url)
	} else if strings.Contains(cause.Error(), "Could not resolve hostname") {
		hint = "Network error. Check:\n" +
			"  1. Your internet connection\n" +
			"  2. The repository URL is correct\n" +
			"  3. DNS is working (try: ping github.com)"
	}

	return &StackError{
		StackName: stackName,
		Operation: "git clone",
		Cause:     cause,
		Hint:      hint,
	}
}

// NewCheckoutError creates an error for git checkout failures
func NewCheckoutError(stackName, ref, refType string, cause error) error {
	hint := fmt.Sprintf("The %s '%s' does not exist in the repository.\n", refType, ref) +
		"Check that:\n" +
		"  1. The tag/commit exists in the remote repository\n" +
		"  2. The branch has been fetched (remote stacks use shallow clones)\n" +
		"  3. The version in your .env file matches an actual release"

	if refType == "tag" {
		hint += fmt.Sprintf("\n\nTo list available tags, run:\n  git ls-remote --tags <repo-url>")
	}

	return &StackError{
		StackName: stackName,
		Operation: fmt.Sprintf("git checkout %s %s", refType, ref),
		Cause:     cause,
		Hint:      hint,
	}
}

// NewVersionRefError creates an error for version ref resolution failures
func NewVersionRefError(stackName, ref, envVar string) error {
	hint := fmt.Sprintf("The environment variable '%s' is not set in your .env file.\n", envVar) +
		"To fix this:\n" +
		fmt.Sprintf("  1. Add '%s=<version>' to your .env file\n", envVar) +
		"  2. Or update stackr.yaml to use a static version instead of ${" + envVar + "}"

	return &StackError{
		StackName: stackName,
		Operation: "resolve version ref",
		Cause:     fmt.Errorf("environment variable ${%s} is not set", envVar),
		Hint:      hint,
	}
}

// NewPullError creates an error for git pull failures
func NewPullError(stackName string, cause error) error {
	hint := "Failed to pull latest changes. This is not critical - using cached version.\n" +
		"If you need the latest changes:\n" +
		"  1. Check your network connection\n" +
		"  2. Verify SSH access to the repository\n" +
		"  3. Try manually pulling: cd .stackr-repos/" + stackName + " && git pull"

	return &StackError{
		StackName: stackName,
		Operation: "git pull",
		Cause:     cause,
		Hint:      hint,
	}
}
