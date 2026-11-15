// Package store centralizes Resumeee persistence domain rules, validation,
// state transitions, quota accounting, and unified job orchestration over a
// pure persistence Backend SPI.
package store

import (
	"time"
)

// Option configures optional Store behavior such as a deterministic test clock.
type Option func(*Store)

// WithClock overrides the UTC wall clock used for timestamps, windows, and leases.
func WithClock(clock func() time.Time) Option {
	return func(s *Store) {
		s.clock = clock
	}
}

// Store coordinates all domain persistence repositories over a Backend SPI.
type Store struct {
	backend   Backend
	clock     func() time.Time
	users     *Users
	presets   *Presets
	templates *Templates
	quotas    *Quotas
	profiles  *Profiles
	career    *Career
	branches  *Branches
	jobs      *Jobs
}

// New constructs a Store backed by backend and optional configuration options.
func New(backend Backend, opts ...Option) *Store {
	s := &Store{backend: backend}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	s.users = &Users{store: s}
	s.presets = &Presets{store: s}
	s.templates = &Templates{store: s}
	s.quotas = &Quotas{store: s}
	s.profiles = &Profiles{store: s}
	s.career = &Career{store: s}
	s.branches = &Branches{store: s}
	s.jobs = &Jobs{store: s}
	return s
}

// Users returns the tenant user repository.
func (s *Store) Users() *Users { return s.users }

// Presets returns the tailoring preset repository.
func (s *Store) Presets() *Presets { return s.presets }

// Templates returns the resume template repository.
func (s *Store) Templates() *Templates { return s.templates }

// Quotas returns the tenant LLM quota and telemetry repository.
func (s *Store) Quotas() *Quotas { return s.quotas }

// Profiles returns the candidate profile and snapshot repository.
func (s *Store) Profiles() *Profiles { return s.profiles }

// Career returns the Tier-1 career graph entity repository.
func (s *Store) Career() *Career { return s.career }

// Branches returns the targeted branch and version repository.
func (s *Store) Branches() *Branches { return s.branches }

// Jobs returns the unified background queue repository.
func (s *Store) Jobs() *Jobs { return s.jobs }

// Close releases underlying storage backend resources.
func (s *Store) Close() error {
	if s.backend == nil {
		return nil
	}
	return s.backend.Close()
}

// now returns the current UTC timestamp using the configured clock or time.Now.
func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

// resolveNow returns explicit in UTC when non-zero, otherwise falling back to s.now().
func (s *Store) resolveNow(explicit time.Time) time.Time {
	if !explicit.IsZero() {
		return explicit.UTC()
	}
	return s.now()
}
