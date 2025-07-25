package integrationtest

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

// assertContainerLogs reads container logs and asserts that all expectedMessages are present.
func assertContainerLogs(t *testing.T, cli *client.Client, cid string, expectedMessages ...string) {
	logReader, err := cli.ContainerLogs(t.Context(), cid, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
	})
	require.NoError(t, err, "Failed to get container logs")
	defer logReader.Close()

	reader := bufio.NewReader(logReader)
	var allLogs strings.Builder
	messagesFound := make([]bool, len(expectedMessages))

	for {
		header := make([]byte, 8)
		_, err := io.ReadFull(reader, header)
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "Error reading Docker log header")

		count := binary.BigEndian.Uint32(header[4:])
		if count == 0 {
			continue
		}

		line := make([]byte, count)
		_, err = io.ReadFull(reader, line)
		require.NoError(t, err, "Error reading Docker log content")

		logLine := string(line)
		t.Logf("DOCKER LOG: %q", logLine)
		allLogs.WriteString(logLine)

		for i, msg := range expectedMessages {
			if !messagesFound[i] && strings.Contains(logLine, msg) {
				messagesFound[i] = true
			}
		}
	}

	t.Logf("Full collected logs:\n%s", allLogs.String())

	for i, found := range messagesFound {
		require.Truef(t, found, "Container logs do not contain the expected message: %q", expectedMessages[i])
	}
}

// assertContainerFileContent checks that the file at containerPath inside the container with ID cid
// contains the expectedContent substring.
func assertContainerFileContent(t *testing.T, cli *client.Client, cid, containerPath, expectedContent string) {
	reader, _, err := cli.CopyFromContainer(t.Context(), cid, containerPath)
	require.NoError(t, err, "Failed to copy file from container")
	defer reader.Close()

	var buf bytes.Buffer
	// CopyFromContainer returns a tar archive. We need to extract the file contents:
	tr := bufio.NewReader(reader)
	_, err = io.Copy(&buf, tr)
	require.NoError(t, err, "Failed to read file content from container")

	content := buf.String()
	require.Contains(t, content, expectedContent, "Container file %q does not contain expected content", containerPath)
}
