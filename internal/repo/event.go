package repo

import "time"

// Event kinds emitted by the daemon. The list is open — handlers should treat
// unknown kinds as informational rather than rejecting them.
const (
	EventKindStackAdded     = "stack.added"
	EventKindStackRemoved   = "stack.removed"
	EventKindStackChanged   = "stack.changed"
	EventKindDeployStarted  = "deploy.started"
	EventKindDeploySucceeded = "deploy.succeeded"
	EventKindDeployFailed   = "deploy.failed"
	EventKindCronStarted    = "cron.started"
	EventKindCronSucceeded  = "cron.succeeded"
	EventKindCronFailed     = "cron.failed"
	EventKindRemoteSynced   = "remote.synced"
	EventKindRemoteFailed   = "remote.failed"
)

// Event is a structured log line persisted for observability.
type Event struct {
	ID        string    `gorm:"primaryKey"`
	Kind      string    `gorm:"index;not null"`
	Stack     string    `gorm:"index;not null;default:''"`
	Message   string    `gorm:"type:text;not null;default:''"`
	CreatedAt time.Time `gorm:"index;not null;autoCreateTime"`
}
