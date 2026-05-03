package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/FyrmForge/hamr/pkg/auth"
	"gorm.io/gorm"
)

// Store implements repo.Store using SQLite via GORM.
type Store struct {
	db *gorm.DB
}

// NewStore creates a new SQLite-backed store.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// Health checks the database connection.
func (s *Store) Health(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// DB returns the underlying GORM database connection.
func (s *Store) DB() *gorm.DB {
	return s.db
}

// ---------------------------------------------------------------------------
// auth.SessionStore implementation
// ---------------------------------------------------------------------------

type sessionRow struct {
	ID        string    `gorm:"primaryKey"`
	SubjectID *string   `gorm:"index"`
	Token     string    `gorm:"uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
}

func (sessionRow) TableName() string { return "sessions" }

// Create inserts a new session row.
// To store extra metadata, add your own columns to the sessions table and
// populate them here, or store a JSON blob if you prefer.
func (s *Store) Create(ctx context.Context, session *auth.Session) error {
	row := sessionRow{
		ID:        session.ID,
		Token:     session.Token,
		ExpiresAt: session.ExpiresAt,
		CreatedAt: session.CreatedAt,
	}
	if session.SubjectID != "" {
		row.SubjectID = &session.SubjectID
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *Store) GetByToken(ctx context.Context, token string) (*auth.Session, error) {
	var row sessionRow
	err := s.db.WithContext(ctx).Where("token = ?", token).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	subjectID := ""
	if row.SubjectID != nil {
		subjectID = *row.SubjectID
	}
	return &auth.Session{
		ID:        row.ID,
		SubjectID: subjectID,
		Token:     row.Token,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&sessionRow{}).Error
}

func (s *Store) DeleteBySubjectID(ctx context.Context, subjectID string) error {
	return s.db.WithContext(ctx).Where("subject_id = ?", subjectID).Delete(&sessionRow{}).Error
}
