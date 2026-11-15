// Package memory provides an in-memory persistence Backend implementation for the Resumeee store.
package memory

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/seed"
	"github.com/resumeee-app/resumeee-core/store"
)

// Compile-time interface satisfaction checks for Backend and transaction handles.
var (
	_ store.Backend = (*Backend)(nil)
	_ store.ReadTx  = (*readTx)(nil)
	_ store.Tx      = (*writeTx)(nil)
)

// dataSet holds the complete in-memory collections stored by a Backend.
type dataSet struct {
	users          map[string]core.User
	presets        map[string]core.Preset
	templates      map[string]core.TemplateSet
	quotas         map[string]core.TenantQuota
	usageRecords   map[string]core.LLMUsageRecord
	profiles       map[string]core.Profile
	skills         map[string]core.Skill
	experiences    map[string]core.WorkExperience
	projects       map[string]core.Project
	educations     map[string]core.Education
	certifications map[string]core.Certification
	facts          map[string]core.Fact
	branches       map[string]core.Branch
	versions       map[string]core.BranchVersion
	jobs           map[string]core.Job
}

// Backend implements store.Backend using deep-copied transactional snapshots guarded by an RWMutex.
type Backend struct {
	mu   sync.RWMutex
	data *dataSet
}

// NewBackend creates an initialized, empty in-memory storage Backend.
func NewBackend() *Backend {
	return &Backend{data: newDataSet()}
}

// New constructs an initialized in-memory store.Store.
func New() *store.Store {
	return store.New(NewBackend())
}

// NewWithClock constructs an in-memory store.Store configured with a deterministic wall clock.
func NewWithClock(clock func() time.Time) *store.Store {
	return store.New(NewBackend(), store.WithClock(clock))
}

// NewSeeded creates an initialized in-memory store.Store populated with default templates,
// presets, global skills, and the default tenant profile and branch.
func NewSeeded(ctx context.Context, defaultUserID ...string) (*store.Store, error) {
	st := New()
	if err := seed.DefaultData(ctx, st, defaultUserID...); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// Read executes fn inside a read-locked snapshot transaction.
func (b *Backend) Read(ctx context.Context, fn func(tx store.ReadTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return fn(&readTx{data: b.data})
}

// Write executes fn against an isolated cloned dataset and commits atomically only if fn returns nil.
func (b *Backend) Write(ctx context.Context, fn func(tx store.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	draft := cloneDataSet(b.data)
	if err := fn(&writeTx{readTx: readTx{data: draft}}); err != nil {
		return err
	}
	b.data = draft
	return nil
}

// Close satisfies store.Backend; in-memory storage holds no external resources.
func (b *Backend) Close() error {
	return nil
}

// newDataSet allocates empty maps for every stored entity collection.
func newDataSet() *dataSet {
	return &dataSet{
		users:          make(map[string]core.User),
		presets:        make(map[string]core.Preset),
		templates:      make(map[string]core.TemplateSet),
		quotas:         make(map[string]core.TenantQuota),
		usageRecords:   make(map[string]core.LLMUsageRecord),
		profiles:       make(map[string]core.Profile),
		skills:         make(map[string]core.Skill),
		experiences:    make(map[string]core.WorkExperience),
		projects:       make(map[string]core.Project),
		educations:     make(map[string]core.Education),
		certifications: make(map[string]core.Certification),
		facts:          make(map[string]core.Fact),
		branches:       make(map[string]core.Branch),
		versions:       make(map[string]core.BranchVersion),
		jobs:           make(map[string]core.Job),
	}
}

// cloneDataSet creates an independent deep copy of every map inside src for atomic Write commits.
func cloneDataSet(src *dataSet) *dataSet {
	return &dataSet{
		users:          cloneMap(src.users),
		presets:        cloneMap(src.presets),
		templates:      cloneMap(src.templates),
		quotas:         cloneMap(src.quotas),
		usageRecords:   cloneMap(src.usageRecords),
		profiles:       cloneMap(src.profiles),
		skills:         cloneMap(src.skills),
		experiences:    cloneMap(src.experiences),
		projects:       cloneMap(src.projects),
		educations:     cloneMap(src.educations),
		certifications: cloneMap(src.certifications),
		facts:          cloneMap(src.facts),
		branches:       cloneMap(src.branches),
		versions:       cloneMap(src.versions),
		jobs:           cloneMap(src.jobs),
	}
}

// cloneMap produces a deep-copied map with cloned element values.
func cloneMap[T any](src map[string]T) map[string]T {
	dst := make(map[string]T, len(src))
	for k, v := range src {
		dst[k] = cloneValue(v)
	}
	return dst
}

// cloneValue produces a deep copy of v via JSON round-trip to prevent shared slice or pointer aliasing.
func cloneValue[T any](v T) T {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}
