package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/memory"
	"github.com/resumeee-app/resumeee-core/store"
	"github.com/resumeee-app/resumeee-core/storetest"
)

// TestCompliance runs the full storetest compliance suite against the in-memory backend.
func TestCompliance(t *testing.T) {
	storetest.RunAll(t, func(_ *testing.T, clock func() time.Time) *store.Store {
		if clock == nil {
			return memory.New()
		}
		return memory.NewWithClock(clock)
	})
}

// TestBackend_WriteRollback verifies that failed Write transactions roll back every partial mutation.
func TestBackend_WriteRollback(t *testing.T) {
	ctx := context.Background()
	backend := memory.NewBackend()
	st := store.New(backend)

	if err := st.Users().UpsertUser(ctx, &core.User{ID: "u1", Email: "before@example.com", FullName: "Before"}); err != nil {
		t.Fatalf("seed user failed: %v", err)
	}

	sentinel := errors.New("abort write")
	err := backend.Write(ctx, func(tx store.Tx) error {
		if putErr := tx.PutUser(ctx, &core.User{ID: "u1", Email: "after@example.com", FullName: "After"}); putErr != nil {
			return putErr
		}
		if putErr := tx.PutPreset(ctx, &core.Preset{ID: "p-rollback", UserID: "u1", Name: "Partial"}); putErr != nil {
			return putErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel abort error, got %v", err)
	}

	u, err := st.Users().User(ctx, "u1")
	if err != nil || u.Email != "before@example.com" {
		t.Fatalf("expected user unchanged after rollback, got %+v (err=%v)", u, err)
	}
	if _, err := st.Presets().Preset(ctx, "u1", "p-rollback"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected partial preset rolled back, got %v", err)
	}
}

// TestNewSeeded verifies that NewSeeded populates built-ins and the default tenant branch.
func TestNewSeeded(t *testing.T) {
	ctx := context.Background()
	st, err := memory.NewSeeded(ctx, "seeded-user")
	if err != nil {
		t.Fatalf("NewSeeded failed: %v", err)
	}
	defer func() { _ = st.Close() }()

	prof, err := st.Profiles().Profile(ctx, "seeded-user")
	if err != nil || prof.MainRevision < 1 {
		t.Fatalf("expected seeded profile, got %+v (err=%v)", prof, err)
	}
	br, err := st.Branches().Branch(ctx, "seeded-user", core.DefaultBranchID("seeded-user"))
	if err != nil || br.ActiveVersionID == nil {
		t.Fatalf("expected active default branch, got %+v (err=%v)", br, err)
	}
}

// TestConcurrentMixedReadsAndWrites exercises parallel Read and Write transactions under race detection.
func TestConcurrentMixedReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	if err := st.Profiles().UpsertProfile(ctx, &core.Profile{UserID: "u1", FullName: "Concurrent"}); err != nil {
		t.Fatalf("UpsertProfile failed: %v", err)
	}

	var wg sync.WaitGroup
	for range 12 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = st.Profiles().IncrementMainRevision(ctx, "u1")
		}()
		go func() {
			defer wg.Done()
			_, _ = st.Profiles().Snapshot(ctx, "u1")
		}()
	}
	wg.Wait()

	prof, err := st.Profiles().Profile(ctx, "u1")
	if err != nil || prof.MainRevision != 13 {
		t.Fatalf("expected revision 13 after 12 increments, got %+v (err=%v)", prof, err)
	}
}
