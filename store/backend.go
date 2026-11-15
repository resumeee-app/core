package store

import (
	"context"
	"time"

	"github.com/resumeee-app/resumeee-core"
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
	// GetUser loads a user by ID.
	GetUser(ctx context.Context, id string) (*core.User, error)

	// ListPresets returns presets belonging to userID or system presets (UserID == core.SystemUserID or "").
	ListPresets(ctx context.Context, userID string) ([]core.Preset, error)

	// GetPreset loads a preset by ID.
	GetPreset(ctx context.Context, id string) (*core.Preset, error)

	// ListTemplates returns templates belonging to userID or built-in templates (IsBuiltIn || UserID == core.SystemUserID).
	ListTemplates(ctx context.Context, userID string) ([]core.TemplateSet, error)

	// GetTemplate loads a template set by ID.
	GetTemplate(ctx context.Context, id string) (*core.TemplateSet, error)

	// GetQuota loads quota state for userID.
	GetQuota(ctx context.Context, userID string) (*core.TenantQuota, error)

	// GetUsageRecord loads an LLM usage record by ID.
	GetUsageRecord(ctx context.Context, id string) (*core.LLMUsageRecord, error)

	// ListUsageRecords returns usage records for userID ordered newest-first up to limit when limit > 0.
	ListUsageRecords(ctx context.Context, userID string, limit int) ([]core.LLMUsageRecord, error)

	// GetProfile loads a candidate profile root by userID.
	GetProfile(ctx context.Context, userID string) (*core.Profile, error)

	// ListSkills returns skills owned by userID, optionally including global skills when includeGlobal is true.
	ListSkills(ctx context.Context, userID string, includeGlobal bool) ([]core.Skill, error)

	// GetSkill loads a skill by ID.
	GetSkill(ctx context.Context, id string) (*core.Skill, error)

	// ListExperiences returns all work experiences owned by userID.
	ListExperiences(ctx context.Context, userID string) ([]core.WorkExperience, error)

	// GetExperience loads a work experience by ID.
	GetExperience(ctx context.Context, id string) (*core.WorkExperience, error)

	// ListProjects returns all projects owned by userID.
	ListProjects(ctx context.Context, userID string) ([]core.Project, error)

	// GetProject loads a project by ID.
	GetProject(ctx context.Context, id string) (*core.Project, error)

	// ListEducations returns all education entries owned by userID.
	ListEducations(ctx context.Context, userID string) ([]core.Education, error)

	// GetEducation loads an education entry by ID.
	GetEducation(ctx context.Context, id string) (*core.Education, error)

	// ListCertifications returns all certifications owned by userID.
	ListCertifications(ctx context.Context, userID string) ([]core.Certification, error)

	// GetCertification loads a certification by ID.
	GetCertification(ctx context.Context, id string) (*core.Certification, error)

	// ListFacts returns all facts owned by userID.
	ListFacts(ctx context.Context, userID string) ([]core.Fact, error)

	// ListFactsByOwner returns facts owned by userID associated with ownerType and ownerID.
	ListFactsByOwner(ctx context.Context, userID string, ownerType core.OwnerType, ownerID string) ([]core.Fact, error)

	// GetFact loads a fact by ID.
	GetFact(ctx context.Context, id string) (*core.Fact, error)

	// ListBranches returns all branches owned by userID.
	ListBranches(ctx context.Context, userID string) ([]core.Branch, error)

	// GetBranch loads a branch by ID.
	GetBranch(ctx context.Context, id string) (*core.Branch, error)

	// ListVersionsByBranch returns all versions belonging to branchID.
	ListVersionsByBranch(ctx context.Context, branchID string) ([]core.BranchVersion, error)

	// ListVersionsByIDs returns versions matching ids.
	ListVersionsByIDs(ctx context.Context, ids []string) ([]core.BranchVersion, error)

	// GetVersion loads a branch version by ID.
	GetVersion(ctx context.Context, id string) (*core.BranchVersion, error)

	// GetVersionByNumber loads a branch version by branchID and versionNumber.
	GetVersionByNumber(ctx context.Context, branchID string, versionNumber int) (*core.BranchVersion, error)

	// GetJob loads a queued background job by ID.
	GetJob(ctx context.Context, id string) (*core.Job, error)

	// ListActiveJobs returns all jobs whose Status is JobStatusPending or JobStatusProcessing.
	ListActiveJobs(ctx context.Context) ([]core.Job, error)

	// ListJobsByBranch returns all jobs whose Tailor payload targets branchID.
	ListJobsByBranch(ctx context.Context, branchID string) ([]core.Job, error)

	// ListTerminalJobsBefore returns terminal jobs owned by userID completed/updated strictly before cutoff.
	ListTerminalJobsBefore(ctx context.Context, userID string, cutoff time.Time) ([]core.Job, error)
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
