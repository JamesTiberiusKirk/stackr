package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryImagePull_Success(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	attempts := 0
	operation := func() error {
		attempts++
		return nil // Succeed immediately
	}

	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Backoff:      2.0,
	}

	err := RetryImagePull(ctx, operation, cfg)
	require.NoError(t, err)
	require.Equal(t, 1, attempts)
}

func TestRetryImagePull_SuccessAfterRetries(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	attempts := 0
	operation := func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary failure")
		}
		return nil // Succeed on 3rd attempt
	}

	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Backoff:      2.0,
	}

	start := time.Now()
	err := RetryImagePull(ctx, operation, cfg)
	duration := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, 3, attempts)
	// Should have waited: 10ms + 20ms = 30ms (plus some overhead)
	require.Greater(t, duration, 30*time.Millisecond)
}

func TestRetryImagePull_FailureAfterMaxAttempts(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	attempts := 0
	operation := func() error {
		attempts++
		return errors.New("persistent failure")
	}

	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Backoff:      2.0,
	}

	err := RetryImagePull(ctx, operation, cfg)
	require.Error(t, err)
	require.Equal(t, 3, attempts)
	require.Contains(t, err.Error(), "failed after 3 attempts")
}

func TestRetryImagePull_ContextCancellation(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	operation := func() error {
		attempts++
		if attempts == 2 {
			cancel() // Cancel after first retry
		}
		return errors.New("failure")
	}

	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     200 * time.Millisecond,
		Backoff:      2.0,
	}

	err := RetryImagePull(ctx, operation, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "retry cancelled")
	// Should have stopped after 2 attempts due to cancellation
	require.LessOrEqual(t, attempts, 2)
}

func TestRetryImagePull_ExponentialBackoff(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	var delays []time.Duration
	lastTime := time.Now()

	attempts := 0
	operation := func() error {
		now := time.Now()
		if attempts > 0 {
			delays = append(delays, now.Sub(lastTime))
		}
		lastTime = now
		attempts++
		return errors.New("failure")
	}

	cfg := RetryConfig{
		MaxAttempts:  4,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Backoff:      2.0,
	}

	err := RetryImagePull(ctx, operation, cfg)
	require.Error(t, err)
	require.Equal(t, 4, attempts)
	require.Len(t, delays, 3)

	// Verify exponential backoff: ~10ms, ~20ms, ~40ms (capped or not)
	require.Greater(t, delays[0], 8*time.Millisecond)
	require.Less(t, delays[0], 15*time.Millisecond)

	require.Greater(t, delays[1], 18*time.Millisecond)
	require.Less(t, delays[1], 25*time.Millisecond)

	require.Greater(t, delays[2], 35*time.Millisecond)
}

func TestRetryImagePull_MaxDelayRespected(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	var delays []time.Duration
	lastTime := time.Now()

	attempts := 0
	operation := func() error {
		now := time.Now()
		if attempts > 0 {
			delays = append(delays, now.Sub(lastTime))
		}
		lastTime = now
		attempts++
		return errors.New("failure")
	}

	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 20 * time.Millisecond,
		MaxDelay:     30 * time.Millisecond, // Cap delay
		Backoff:      2.0,
	}

	err := RetryImagePull(ctx, operation, cfg)
	require.Error(t, err)

	// All delays after first should be capped at MaxDelay
	for i, delay := range delays {
		if i > 0 { // After first retry
			require.LessOrEqual(t, delay, 35*time.Millisecond) // Allow some overhead
		}
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	t.Helper()
	cfg := DefaultRetryConfig()

	require.Equal(t, 5, cfg.MaxAttempts)
	require.Equal(t, 30*time.Second, cfg.InitialDelay)
	require.Equal(t, 5*time.Minute, cfg.MaxDelay)
	require.Equal(t, 2.0, cfg.Backoff)
}
