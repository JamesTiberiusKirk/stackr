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
		&InviteToken{},
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

// InviteToken is the GORM mapping for invite_tokens. UserID has an index
// (not a foreign key constraint — sqlite + gorm's automigrate keep this
// loose; cascade is enforced application-side in DeleteUser).
type InviteToken struct {
	ID        string    `gorm:"primaryKey"`
	UserID    string    `gorm:"index;not null"`
	Token     string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Used      bool      `gorm:"not null;default:false"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime"`
}

// User represents an application user.
type User struct {
	ID           string    `gorm:"primaryKey"`
	Email        string    `gorm:"uniqueIndex;not null"`
	PasswordHash string    `gorm:"not null;default:''"`
	Name         string    `gorm:"not null;default:''"`
	Role         string    `gorm:"not null;default:'user'"`
	Active       bool      `gorm:"not null;default:true"`
	TOTPSecret   string    `gorm:"not null;default:''"`
	TOTPEnabled  bool      `gorm:"not null;default:false"`
	CreatedAt    time.Time `gorm:"not null;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"not null;autoUpdateTime"`
}
