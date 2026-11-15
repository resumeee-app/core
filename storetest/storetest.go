// Package storetest provides a backend-agnostic compliance suite verifying that
// any store.Backend wired into store.Store enforces all Resumeee persistence
// invariants, domain rules, concurrency guarantees, and queue semantics.
package storetest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core/store"
)

// Factory creates an isolated, empty store.Store configured with the supplied clock
// (or the real wall clock when clock is nil) for a single compliance test case.
type Factory func(t *testing.T, clock func() time.Time) *store.Store

// testClock provides thread-safe deterministic time control across compliance tests.
type testClock struct {
	mu  sync.Mutex
	cur time.Time
}

// newTestClock constructs a testClock initialized to start in UTC.
func newTestClock(start time.Time) *testClock {
	return &testClock{cur: start.UTC()}
}

// Now returns the current clock timestamp in UTC.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

// Advance moves the clock forward by d and returns the updated timestamp.
func (c *testClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
	return c.cur
}

// defaultTestBaseTime fixes a reproducible starting instant for deterministic clock tests.
var defaultTestBaseTime = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// openStore invokes newStore and registers Close cleanup on t.
func openStore(t *testing.T, newStore Factory, clock func() time.Time) *store.Store {
	t.Helper()
	st := newStore(t, clock)
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close failed: %v", err)
		}
	})
	return st
}

// runCtx returns a background context bound cleanly with a safety timeout for tests.
func runCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// RunAll executes the complete compliance suite against a storage Factory.
func RunAll(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("Users", func(t *testing.T) { RunUsers(t, newStore) })
	t.Run("PresetsAndTemplates", func(t *testing.T) { RunPresetsAndTemplates(t, newStore) })
	t.Run("Quotas", func(t *testing.T) { RunQuotas(t, newStore) })
	t.Run("ProfilesAndCareer", func(t *testing.T) { RunProfilesAndCareer(t, newStore) })
	t.Run("Branches", func(t *testing.T) { RunBranches(t, newStore) })
	t.Run("JobQueue", func(t *testing.T) { RunJobQueue(t, newStore) })
	t.Run("Seeding", func(t *testing.T) { RunSeeding(t, newStore) })
}
