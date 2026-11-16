package store

import (
	"context"

	"github.com/resumeee-app/core"
)

// Backend defines the pure persistence and transaction contract that storage backends
// (memory, SQL, Firestore) implement without embedding domain rules.
type Backend interface {
	// Read executes fn inside a consistent read-only transaction.
	Read(ctx context.Context, fn func(tx ReadTx) error) error

	// Write executes fn inside an atomic read-write transaction. Backends with optimistic
	// concurrency (Firestore, Postgres SERIALIZABLE) may retry fn on transient conflicts.
	Write(ctx context.Context, fn func(tx Tx) error) error

	// Close releases underlying storage resources.
	Close() error
}

// ReadTx exposes side-effect-free CRUD reads and indexed equality/IN queries within a transaction.
// Implementations must return core.ErrNotFound when a requested single record does not exist.
type ReadTx interface {
	// User loads a user by ID.
	User(ctx context.Context, id string) (*core.User, error)

	// ListPresets returns presets whose UserID matches userIDs or whose ID starts with any prefix in idPrefixes, deduplicated.
	ListPresets(ctx context.Context, userIDs []string, idPrefixes []string) ([]core.Preset, error)

	// Preset loads a preset by ID.
	Preset(ctx context.Context, id string) (*core.Preset, error)

	// ListTemplates returns templates whose UserID matches userIDs, whose ID starts with any prefix in idPrefixes, or whose IsBuiltIn flag is true when includeBuiltIn is true, deduplicated.
	ListTemplates(ctx context.Context, userIDs []string, idPrefixes []string, includeBuiltIn bool) ([]core.TemplateSet, error)

	// Template loads a template set by ID.
	Template(ctx context.Context, id string) (*core.TemplateSet, error)

	// Quota loads quota state for userID.
	Quota(ctx context.Context, userID string) (*core.TenantQuota, error)

	// UsageRecord loads an LLM usage record by ID.
	UsageRecord(ctx context.Context, id string) (*core.LLMUsageRecord, error)

	// ListUsageRecords returns usage records for userID ordered newest-first up to limit when limit > 0.
	ListUsageRecords(ctx context.Context, userID string, limit int) ([]core.LLMUsageRecord, error)

	// Profile loads a candidate profile root by userID.
	Profile(ctx context.Context, userID string) (*core.Profile, error)

	// ListSkills returns skills owned by userID, optionally including global skills when includeGlobal is true.
	ListSkills(ctx context.Context, userID string, includeGlobal bool) ([]core.Skill, error)

	// Skill loads a skill by ID.
	Skill(ctx context.Context, id string) (*core.Skill, error)

	// ListExperiences returns all work experiences owned by userID.
	ListExperiences(ctx context.Context, userID string) ([]core.WorkExperience, error)

	// Experience loads a work experience by ID.
	Experience(ctx context.Context, id string) (*core.WorkExperience, error)

	// ListProjects returns all projects owned by userID.
	ListProjects(ctx context.Context, userID string) ([]core.Project, error)

	// Project loads a project by ID.
	Project(ctx context.Context, id string) (*core.Project, error)

	// ListEducations returns all education entries owned by userID.
	ListEducations(ctx context.Context, userID string) ([]core.Education, error)

	// Education loads an education entry by ID.
	Education(ctx context.Context, id string) (*core.Education, error)

	// ListCertifications returns all certifications owned by userID.
	ListCertifications(ctx context.Context, userID string) ([]core.Certification, error)

	// Certification loads a certification by ID.
	Certification(ctx context.Context, id string) (*core.Certification, error)

	// ListFacts returns all facts owned by userID.
	ListFacts(ctx context.Context, userID string) ([]core.Fact, error)

	// ListFactsByOwner returns facts owned by userID associated with ownerType and ownerID.
	ListFactsByOwner(ctx context.Context, userID string, ownerType core.OwnerType, ownerID string) ([]core.Fact, error)

	// Fact loads a fact by ID.
	Fact(ctx context.Context, id string) (*core.Fact, error)

	// ListBranches returns all branches owned by userID.
	ListBranches(ctx context.Context, userID string) ([]core.Branch, error)

	// Branch loads a branch by ID.
	Branch(ctx context.Context, id string) (*core.Branch, error)

	// ListVersionsByBranch returns all versions belonging to branchID.
	ListVersionsByBranch(ctx context.Context, branchID string) ([]core.BranchVersion, error)

	// ListVersionsByIDs returns versions matching ids.
	ListVersionsByIDs(ctx context.Context, ids []string) ([]core.BranchVersion, error)

	// Version loads a branch version by ID.
	Version(ctx context.Context, id string) (*core.BranchVersion, error)

	// VersionByNumber loads a branch version by branchID and versionNumber.
	VersionByNumber(ctx context.Context, branchID string, versionNumber int) (*core.BranchVersion, error)

	// Job loads a queued background job by ID.
	Job(ctx context.Context, id string) (*core.Job, error)

	// ListActiveJobs returns all jobs whose Status is JobStatusPending or JobStatusProcessing.
	ListActiveJobs(ctx context.Context) ([]core.Job, error)

	// ListJobsByBranch returns all jobs whose Tailor payload targets branchID.
	ListJobsByBranch(ctx context.Context, branchID string) ([]core.Job, error)

	// ListJobsByUser returns jobs owned by userID matching any status in statuses, or all jobs owned by userID when statuses is empty.
	ListJobsByUser(ctx context.Context, userID string, statuses []core.JobStatus) ([]core.Job, error)
}

// Tx extends ReadTx with pure persistence writes for use inside Backend.Write transactions.
// Delete operations must return core.ErrNotFound when the target record does not exist.
type Tx interface {
	ReadTx

	// PutUser inserts or replaces a user record.
	PutUser(ctx context.Context, user *core.User) error

	// PutPreset inserts or replaces a preset record.
	PutPreset(ctx context.Context, preset *core.Preset) error

	// DeletePreset removes a preset record by ID.
	DeletePreset(ctx context.Context, id string) error

	// PutTemplate inserts or replaces a template set record.
	PutTemplate(ctx context.Context, tmpl *core.TemplateSet) error

	// DeleteTemplate removes a template set record by ID.
	DeleteTemplate(ctx context.Context, id string) error

	// PutQuota inserts or replaces a tenant quota record.
	PutQuota(ctx context.Context, quota *core.TenantQuota) error

	// PutUsageRecord inserts or replaces an LLM usage reservation/telemetry record.
	PutUsageRecord(ctx context.Context, rec *core.LLMUsageRecord) error

	// PutProfile inserts or replaces a candidate profile root record.
	PutProfile(ctx context.Context, profile *core.Profile) error

	// PutSkill inserts or replaces a skill record.
	PutSkill(ctx context.Context, skill *core.Skill) error

	// DeleteSkill removes a skill record by ID.
	DeleteSkill(ctx context.Context, id string) error

	// PutExperience inserts or replaces a work experience record.
	PutExperience(ctx context.Context, exp *core.WorkExperience) error

	// DeleteExperience removes a work experience record by ID.
	DeleteExperience(ctx context.Context, id string) error

	// PutProject inserts or replaces a project record.
	PutProject(ctx context.Context, proj *core.Project) error

	// DeleteProject removes a project record by ID.
	DeleteProject(ctx context.Context, id string) error

	// PutEducation inserts or replaces an education record.
	PutEducation(ctx context.Context, edu *core.Education) error

	// DeleteEducation removes an education record by ID.
	DeleteEducation(ctx context.Context, id string) error

	// PutCertification inserts or replaces a certification record.
	PutCertification(ctx context.Context, cert *core.Certification) error

	// DeleteCertification removes a certification record by ID.
	DeleteCertification(ctx context.Context, id string) error

	// PutFact inserts or replaces a fact record.
	PutFact(ctx context.Context, fact *core.Fact) error

	// DeleteFact removes a fact record by ID.
	DeleteFact(ctx context.Context, id string) error

	// PutBranch inserts or replaces a branch record.
	PutBranch(ctx context.Context, branch *core.Branch) error

	// DeleteBranch removes a branch record by ID.
	DeleteBranch(ctx context.Context, id string) error

	// PutVersion inserts or replaces a branch version record.
	PutVersion(ctx context.Context, version *core.BranchVersion) error

	// DeleteVersion removes a branch version record by ID.
	DeleteVersion(ctx context.Context, id string) error

	// PutJob inserts or replaces a background job record.
	PutJob(ctx context.Context, job *core.Job) error

	// DeleteJob removes a background job record by ID.
	DeleteJob(ctx context.Context, id string) error
}
