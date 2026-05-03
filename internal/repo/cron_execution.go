package repo

import "time"

// CronExecution status values.
const (
	CronStatusRunning = "running"
	CronStatusSuccess = "success"
	CronStatusFailed  = "failed"
)

// CronExecution trigger values.
const (
	CronTriggerCron   = "cron"
	CronTriggerManual = "manual"
)

// CronExecution is a single run of a stack-defined cron job.
type CronExecution struct {
	ID         string     `gorm:"primaryKey"`
	Stack      string     `gorm:"index;not null"`
	Service    string     `gorm:"index;not null"`
	Schedule   string     `gorm:"not null"`
	Status     string     `gorm:"index;not null"`
	Trigger    string     `gorm:"not null"`
	Error      string     `gorm:"not null;default:''"`
	Stdout     string     `gorm:"type:text;not null;default:''"`
	Container  string     `gorm:"not null;default:''"`
	StartedAt  time.Time  `gorm:"not null"`
	FinishedAt *time.Time `gorm:""`
}
