package removal

import (
	"sync"
)

// Tracker maintains the current set of known stacks and detects removals
type Tracker struct {
	mu     sync.RWMutex
	stacks map[string]bool
}

// NewTracker creates a new stack tracker
func NewTracker() *Tracker {
	return &Tracker{
		stacks: make(map[string]bool),
	}
}

// Update updates the known stack set and returns newly removed stacks
func (t *Tracker) Update(current []string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	currentSet := make(map[string]bool)
	for _, stack := range current {
		currentSet[stack] = true
	}

	// Find removed stacks (in old set but not in current)
	var removed []string
	for stack := range t.stacks {
		if !currentSet[stack] {
			removed = append(removed, stack)
		}
	}

	t.stacks = currentSet
	return removed
}

// Initialize sets the initial stack state (called at startup)
func (t *Tracker) Initialize(stacks []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stacks = make(map[string]bool)
	for _, stack := range stacks {
		t.stacks[stack] = true
	}
}
