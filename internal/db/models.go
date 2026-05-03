package db

import (
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// models returns all GORM model structs for auto-migration.
func models() []any {
	return []any{
		&Session{},
		&User{},
		&repo.Deployment{},
		&repo.CronExecution{},
		&repo.Event{},
	}
}

// Session represents a user session.
type Session struct {
	ID        string    `gorm:"primaryKey"`
	SubjectID *string   `gorm:"index"`
	Token     string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`
	// Add your own metadata columns here, e.g.:
	// IP        string `gorm:"not null;default:''"`
}

// User represents an application user.
type User struct {
	ID           string    `gorm:"primaryKey"`
	Email        string    `gorm:"uniqueIndex;not null"`
	PasswordHash string    `gorm:"not null"`
	Name         string    `gorm:"not null;default:''"`
	Role         string    `gorm:"not null;default:'user'"`
	Active       bool      `gorm:"not null;default:true"`
	CreatedAt    time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"not null;autoUpdateTime"`
}
