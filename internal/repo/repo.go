package repo

import (
	"context"

	"github.com/FyrmForge/hamr/pkg/auth"
)

// Store defines the data access interface for the application.
type Store interface {
	// Health checks the database connection.
	Health(ctx context.Context) error

	// Session persistence — satisfies auth.SessionStore.
	auth.SessionStore

	// GetUserByID returns a user by ID, or nil if not found.
	GetUserByID(ctx context.Context, id string) (*User, error)

	// GetUserByEmail returns a user by email, or nil if not found.
	GetUserByEmail(ctx context.Context, email string) (*User, error)

	// CreateUser inserts a new user.
	CreateUser(ctx context.Context, user *User) error

	// Deployments.
	CreateDeployment(ctx context.Context, d *Deployment) error
	UpdateDeployment(ctx context.Context, d *Deployment) error
	GetDeploymentByID(ctx context.Context, id string) (*Deployment, error)
	ListDeployments(ctx context.Context, filter DeploymentFilter) ([]Deployment, error)

	// Cron executions.
	CreateCronExecution(ctx context.Context, e *CronExecution) error
	UpdateCronExecution(ctx context.Context, e *CronExecution) error
	GetCronExecutionByID(ctx context.Context, id string) (*CronExecution, error)
	ListCronExecutions(ctx context.Context, filter CronExecutionFilter) ([]CronExecution, error)

	// Events.
	CreateEvent(ctx context.Context, e *Event) error
	ListEvents(ctx context.Context, filter EventFilter) ([]Event, error)
}

// DeploymentFilter narrows ListDeployments results. Zero values mean "no filter".
type DeploymentFilter struct {
	Stack  string
	Status string
	Limit  int
	Offset int
}

// CronExecutionFilter narrows ListCronExecutions results.
type CronExecutionFilter struct {
	Stack   string
	Service string
	Status  string
	Limit   int
	Offset  int
}

// EventFilter narrows ListEvents results.
type EventFilter struct {
	Kind   string
	Stack  string
	Limit  int
	Offset int
}
