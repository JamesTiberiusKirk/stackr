package cronjobs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// CronLogWriters holds separate log file writers for build and execution phases
type CronLogWriters struct {
	BuildLog  io.WriteCloser
	ExecLog   io.WriteCloser
	timestamp string
}

// CreateCronLogWriters creates separate log files for build and execution
// Directory structure: {logsDir}/{stack}/
// Filename format: {service}-{timestamp}.build.log and {service}-{timestamp}.exec.log
func CreateCronLogWriters(logsDir, stack, service string) (*CronLogWriters, error) {
	// Create directory: logs/cron/mystack/
	logDir := filepath.Join(logsDir, stack)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Generate timestamp: 2025-12-28_14-30-00
	timestamp := time.Now().Format("2006-01-02_15-04-05")

	// Create build log: scraper-2025-12-28_14-30-00.build.log
	buildLogPath := filepath.Join(logDir, fmt.Sprintf("%s-%s.build.log", service, timestamp))
	buildFile, err := os.Create(buildLogPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create build log file: %w", err)
	}

	// Create exec log: scraper-2025-12-28_14-30-00.exec.log
	execLogPath := filepath.Join(logDir, fmt.Sprintf("%s-%s.exec.log", service, timestamp))
	execFile, err := os.Create(execLogPath)
	if err != nil {
		_ = buildFile.Close()
		return nil, fmt.Errorf("failed to create exec log file: %w", err)
	}

	return &CronLogWriters{
		BuildLog:  buildFile,
		ExecLog:   execFile,
		timestamp: timestamp,
	}, nil
}

// Close closes both log file writers
func (w *CronLogWriters) Close() error {
	var err1, err2 error
	if w.BuildLog != nil {
		err1 = w.BuildLog.Close()
	}
	if w.ExecLog != nil {
		err2 = w.ExecLog.Close()
	}
	if err1 != nil {
		return err1
	}
	return err2
}

// GenerateContainerName creates a deterministic container name for a cron job
// Format: {stack}-{service}-cron-{timestamp}
func GenerateContainerName(stack, service string) string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("%s-%s-cron-%d", stack, service, timestamp)
}
