package remote

import (
	"context"
	"fmt"
	"log"
	"time"
)

// RetryConfig configures exponential backoff retry behavior
type RetryConfig struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Backoff      float64 // Exponential backoff multiplier
}

// DefaultRetryConfig returns sensible defaults for image pull retries
// Delays: 30s, 1m, 2m, 4m, 5m (max 5 attempts)
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 30 * time.Second,
		MaxDelay:     5 * time.Minute,
		Backoff:      2.0,
	}
}

// RetryImagePull attempts an operation with exponential backoff
// This is designed for image pull operations that may fail when a tag exists
// but the image hasn't been published yet (common in CI/CD workflows)
func RetryImagePull(ctx context.Context, operation func() error, cfg RetryConfig) error {
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		// Attempt the operation
		err := operation()
		if err == nil {
			if attempt > 1 {
				log.Printf("image pull succeeded on attempt %d/%d", attempt, cfg.MaxAttempts)
			}
			return nil
		}

		lastErr = err

		// If this was the last attempt, return the error
		if attempt == cfg.MaxAttempts {
			return fmt.Errorf("image pull failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
		}

		// Log the retry
		log.Printf("image pull failed (attempt %d/%d), retrying in %v: %v",
			attempt, cfg.MaxAttempts, delay, err)

		// Wait before retrying (with context cancellation support)
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

		// Calculate next delay with exponential backoff
		delay = time.Duration(float64(delay) * cfg.Backoff)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}

	return fmt.Errorf("image pull failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
}
