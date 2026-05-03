package repo

import "time"

// Deployment status values.
const (
	DeploymentStatusPending  = "pending"
	DeploymentStatusRunning  = "running"
	DeploymentStatusSuccess  = "success"
	DeploymentStatusFailed   = "failed"
	DeploymentStatusCanceled = "canceled"
)

// Deployment trigger values — what caused the deployment to run.
const (
	DeploymentTriggerManual  = "manual"
	DeploymentTriggerAPI     = "api"
	DeploymentTriggerCron    = "cron"
	DeploymentTriggerWatch   = "watch"
	DeploymentTriggerWebhook = "webhook"
)

// Deployment is a single deployment attempt for a stack.
type Deployment struct {
	ID         string     `gorm:"primaryKey"`
	Stack      string     `gorm:"index;not null"`
	Tag        string     `gorm:"not null;default:''"`
	Status     string     `gorm:"index;not null"`
	Trigger    string     `gorm:"not null"`
	Error      string     `gorm:"not null;default:''"`
	Stdout     string     `gorm:"type:text;not null;default:''"`
	StartedAt  time.Time  `gorm:"not null"`
	FinishedAt *time.Time `gorm:""`
}
