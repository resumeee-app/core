package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// readTx provides side-effect-free read queries against a Backend dataSet snapshot.
type readTx struct {
	data *dataSet
}

// writeTx extends readTx with transactional mutations over an isolated draft dataSet.
type writeTx struct {
	readTx
}

// GetUser loads a user by ID.
func (r *readTx) GetUser(ctx context.Context, id string) (*core.User, error) {
	return getRecord(ctx, r.data.users, id)
}

// ListPresets returns presets belonging to userID or system presets.
func (r *readTx) ListPresets(ctx context.Context, userID string) ([]core.Preset, error) {
	return listWhere(ctx, r.data.presets, func(p core.Preset) bool {
		return p.UserID == "" || p.UserID == core.SystemUserID || p.UserID == userID || p.IsBuiltIn()
	})
}

// GetPreset loads a preset by ID.
func (r *readTx) GetPreset(ctx context.Context, id string) (*core.Preset, error) {
	return getRecord(ctx, r.data.presets, id)
}

// ListTemplates returns templates belonging to userID or built-in system templates.
func (r *readTx) ListTemplates(ctx context.Context, userID string) ([]core.TemplateSet, error) {
	return listWhere(ctx, r.data.templates, func(t core.TemplateSet) bool {
		return t.IsBuiltIn || strings.HasPrefix(t.ID, "builtin-") || t.UserID == core.SystemUserID || t.UserID == userID
	})
}

// GetTemplate loads a template set by ID.
func (r *readTx) GetTemplate(ctx context.Context, id string) (*core.TemplateSet, error) {
	return getRecord(ctx, r.data.templates, id)
}

// GetQuota loads quota state for userID.
func (r *readTx) GetQuota(ctx context.Context, userID string) (*core.TenantQuota, error) {
	return getRecord(ctx, r.data.quotas, userID)
}

// GetUsageRecord loads an LLM usage record by ID.
func (r *readTx) GetUsageRecord(ctx context.Context, id string) (*core.LLMUsageRecord, error) {
	return getRecord(ctx, r.data.usageRecords, id)
}

// ListUsageRecords returns usage records for userID ordered newest-first up to limit when limit > 0.
func (r *readTx) ListUsageRecords(ctx context.Context, userID string, limit int) ([]core.LLMUsageRecord, error) {
	recs, err := listWhere(ctx, r.data.usageRecords, func(rec core.LLMUsageRecord) bool {
		return rec.UserID == userID
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		if !recs[i].CreatedAt.Equal(recs[j].CreatedAt) {
			return recs[i].CreatedAt.After(recs[j].CreatedAt)
		}
		return recs[i].ID > recs[j].ID
	})
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	return recs, nil
}

// GetProfile loads a candidate profile root by userID.
func (r *readTx) GetProfile(ctx context.Context, userID string) (*core.Profile, error) {
	return getRecord(ctx, r.data.profiles, userID)
}

// ListSkills returns skills owned by userID, optionally including global skills when includeGlobal is true.
func (r *readTx) ListSkills(ctx context.Context, userID string, includeGlobal bool) ([]core.Skill, error) {
	return listWhere(ctx, r.data.skills, func(s core.Skill) bool {
		if s.UserID == userID {
			return true
		}
		return includeGlobal && (s.IsGlobal || s.UserID == core.SystemUserID)
	})
}

// GetSkill loads a skill by ID.
func (r *readTx) GetSkill(ctx context.Context, id string) (*core.Skill, error) {
	return getRecord(ctx, r.data.skills, id)
}

// ListExperiences returns all work experiences owned by userID.
func (r *readTx) ListExperiences(ctx context.Context, userID string) ([]core.WorkExperience, error) {
	return listWhere(ctx, r.data.experiences, func(e core.WorkExperience) bool { return e.UserID == userID })
}

// GetExperience loads a work experience by ID.
func (r *readTx) GetExperience(ctx context.Context, id string) (*core.WorkExperience, error) {
	return getRecord(ctx, r.data.experiences, id)
}

// ListProjects returns all projects owned by userID.
func (r *readTx) ListProjects(ctx context.Context, userID string) ([]core.Project, error) {
	return listWhere(ctx, r.data.projects, func(p core.Project) bool { return p.UserID == userID })
}

// GetProject loads a project by ID.
func (r *readTx) GetProject(ctx context.Context, id string) (*core.Project, error) {
	return getRecord(ctx, r.data.projects, id)
}

// ListEducations returns all education entries owned by userID.
func (r *readTx) ListEducations(ctx context.Context, userID string) ([]core.Education, error) {
	return listWhere(ctx, r.data.educations, func(e core.Education) bool { return e.UserID == userID })
}

// GetEducation loads an education entry by ID.
func (r *readTx) GetEducation(ctx context.Context, id string) (*core.Education, error) {
	return getRecord(ctx, r.data.educations, id)
}

// ListCertifications returns all certifications owned by userID.
func (r *readTx) ListCertifications(ctx context.Context, userID string) ([]core.Certification, error) {
	return listWhere(ctx, r.data.certifications, func(c core.Certification) bool { return c.UserID == userID })
}

// GetCertification loads a certification by ID.
func (r *readTx) GetCertification(ctx context.Context, id string) (*core.Certification, error) {
	return getRecord(ctx, r.data.certifications, id)
}

// ListFacts returns all facts owned by userID.
func (r *readTx) ListFacts(ctx context.Context, userID string) ([]core.Fact, error) {
	return listWhere(ctx, r.data.facts, func(f core.Fact) bool { return f.UserID == userID })
}

// ListFactsByOwner returns facts owned by userID associated with ownerType and ownerID.
func (r *readTx) ListFactsByOwner(ctx context.Context, userID string, ownerType core.OwnerType, ownerID string) ([]core.Fact, error) {
	return listWhere(ctx, r.data.facts, func(f core.Fact) bool {
		return f.UserID == userID && f.OwnerType == ownerType && f.OwnerID == ownerID
	})
}

// GetFact loads a fact by ID.
func (r *readTx) GetFact(ctx context.Context, id string) (*core.Fact, error) {
	return getRecord(ctx, r.data.facts, id)
}

// ListBranches returns all branches owned by userID.
func (r *readTx) ListBranches(ctx context.Context, userID string) ([]core.Branch, error) {
	return listWhere(ctx, r.data.branches, func(b core.Branch) bool { return b.UserID == userID })
}

// GetBranch loads a branch by ID.
func (r *readTx) GetBranch(ctx context.Context, id string) (*core.Branch, error) {
	return getRecord(ctx, r.data.branches, id)
}

// ListVersionsByBranch returns all versions belonging to branchID.
func (r *readTx) ListVersionsByBranch(ctx context.Context, branchID string) ([]core.BranchVersion, error) {
	return listWhere(ctx, r.data.versions, func(v core.BranchVersion) bool { return v.BranchID == branchID })
}

// ListVersionsByIDs returns versions matching ids.
func (r *readTx) ListVersionsByIDs(ctx context.Context, ids []string) ([]core.BranchVersion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	return listWhere(ctx, r.data.versions, func(v core.BranchVersion) bool {
		_, ok := want[v.ID]
		return ok
	})
}

// GetVersion loads a branch version by ID.
func (r *readTx) GetVersion(ctx context.Context, id string) (*core.BranchVersion, error) {
	return getRecord(ctx, r.data.versions, id)
}

// GetVersionByNumber loads a branch version by branchID and versionNumber.
func (r *readTx) GetVersionByNumber(ctx context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, v := range r.data.versions {
		if v.BranchID == branchID && v.VersionNumber == versionNumber {
			cp := cloneValue(v)
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

// GetJob loads a queued background job by ID.
func (r *readTx) GetJob(ctx context.Context, id string) (*core.Job, error) {
	return getRecord(ctx, r.data.jobs, id)
}

// ListActiveJobs returns all jobs whose Status is JobStatusPending or JobStatusProcessing.
func (r *readTx) ListActiveJobs(ctx context.Context) ([]core.Job, error) {
	return listWhere(ctx, r.data.jobs, func(j core.Job) bool {
		return j.Status == core.JobStatusPending || j.Status == core.JobStatusProcessing
	})
}

// ListJobsByBranch returns all jobs whose Tailor payload targets branchID.
func (r *readTx) ListJobsByBranch(ctx context.Context, branchID string) ([]core.Job, error) {
	return listWhere(ctx, r.data.jobs, func(j core.Job) bool {
		return j.Tailor != nil && j.Tailor.BranchID == branchID
	})
}

// ListTerminalJobsBefore returns terminal jobs owned by userID completed/updated strictly before cutoff.
func (r *readTx) ListTerminalJobsBefore(ctx context.Context, userID string, cutoff time.Time) ([]core.Job, error) {
	return listWhere(ctx, r.data.jobs, func(j core.Job) bool {
		if j.UserID != userID || !j.Status.IsTerminal() {
			return false
		}
		if j.CompletedAt != nil {
			return j.CompletedAt.Before(cutoff)
		}
		return j.UpdatedAt.Before(cutoff)
	})
}

// PutUser inserts or replaces a user record.
func (w *writeTx) PutUser(ctx context.Context, user *core.User) error {
	if user == nil {
		return fmt.Errorf("%w: user cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.users, user.ID, user)
}

// PutPreset inserts or replaces a preset record.
func (w *writeTx) PutPreset(ctx context.Context, preset *core.Preset) error {
	if preset == nil {
		return fmt.Errorf("%w: preset cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.presets, preset.ID, preset)
}

// DeletePreset removes a preset record by ID.
func (w *writeTx) DeletePreset(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.presets, id)
}

// PutTemplate inserts or replaces a template set record.
func (w *writeTx) PutTemplate(ctx context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.templates, tmpl.ID, tmpl)
}

// DeleteTemplate removes a template set record by ID.
func (w *writeTx) DeleteTemplate(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.templates, id)
}

// PutQuota inserts or replaces a tenant quota record.
func (w *writeTx) PutQuota(ctx context.Context, quota *core.TenantQuota) error {
	if quota == nil {
		return fmt.Errorf("%w: quota cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.quotas, quota.UserID, quota)
}

// PutUsageRecord inserts or replaces an LLM usage reservation/telemetry record.
func (w *writeTx) PutUsageRecord(ctx context.Context, rec *core.LLMUsageRecord) error {
	if rec == nil {
		return fmt.Errorf("%w: usage record cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.usageRecords, rec.ID, rec)
}

// PutProfile inserts or replaces a candidate profile root record.
func (w *writeTx) PutProfile(ctx context.Context, profile *core.Profile) error {
	if profile == nil {
		return fmt.Errorf("%w: profile cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.profiles, profile.UserID, profile)
}

// PutSkill inserts or replaces a skill record.
func (w *writeTx) PutSkill(ctx context.Context, skill *core.Skill) error {
	if skill == nil {
		return fmt.Errorf("%w: skill cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.skills, skill.ID, skill)
}

// DeleteSkill removes a skill record by ID.
func (w *writeTx) DeleteSkill(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.skills, id)
}

// PutExperience inserts or replaces a work experience record.
func (w *writeTx) PutExperience(ctx context.Context, exp *core.WorkExperience) error {
	if exp == nil {
		return fmt.Errorf("%w: experience cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.experiences, exp.ID, exp)
}

// DeleteExperience removes a work experience record by ID.
func (w *writeTx) DeleteExperience(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.experiences, id)
}

// PutProject inserts or replaces a project record.
func (w *writeTx) PutProject(ctx context.Context, proj *core.Project) error {
	if proj == nil {
		return fmt.Errorf("%w: project cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.projects, proj.ID, proj)
}

// DeleteProject removes a project record by ID.
func (w *writeTx) DeleteProject(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.projects, id)
}

// PutEducation inserts or replaces an education record.
func (w *writeTx) PutEducation(ctx context.Context, edu *core.Education) error {
	if edu == nil {
		return fmt.Errorf("%w: education cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.educations, edu.ID, edu)
}

// DeleteEducation removes an education record by ID.
func (w *writeTx) DeleteEducation(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.educations, id)
}

// PutCertification inserts or replaces a certification record.
func (w *writeTx) PutCertification(ctx context.Context, cert *core.Certification) error {
	if cert == nil {
		return fmt.Errorf("%w: certification cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.certifications, cert.ID, cert)
}

// DeleteCertification removes a certification record by ID.
func (w *writeTx) DeleteCertification(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.certifications, id)
}

// PutFact inserts or replaces a fact record.
func (w *writeTx) PutFact(ctx context.Context, fact *core.Fact) error {
	if fact == nil {
		return fmt.Errorf("%w: fact cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.facts, fact.ID, fact)
}

// DeleteFact removes a fact record by ID.
func (w *writeTx) DeleteFact(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.facts, id)
}

// PutBranch inserts or replaces a branch record.
func (w *writeTx) PutBranch(ctx context.Context, branch *core.Branch) error {
	if branch == nil {
		return fmt.Errorf("%w: branch cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.branches, branch.ID, branch)
}

// DeleteBranch removes a branch record by ID.
func (w *writeTx) DeleteBranch(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.branches, id)
}

// PutVersion inserts or replaces a branch version record.
func (w *writeTx) PutVersion(ctx context.Context, version *core.BranchVersion) error {
	if version == nil {
		return fmt.Errorf("%w: version cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.versions, version.ID, version)
}

// DeleteVersion removes a branch version record by ID.
func (w *writeTx) DeleteVersion(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.versions, id)
}

// PutJob inserts or replaces a background job record.
func (w *writeTx) PutJob(ctx context.Context, job *core.Job) error {
	if job == nil {
		return fmt.Errorf("%w: job cannot be nil", core.ErrValidation)
	}
	return putRecord(ctx, w.data.jobs, job.ID, job)
}

// DeleteJob removes a background job record by ID.
func (w *writeTx) DeleteJob(ctx context.Context, id string) error {
	return deleteRecord(ctx, w.data.jobs, id)
}

// getRecord fetches a cloned record from m or returns core.ErrNotFound.
func getRecord[T any](ctx context.Context, m map[string]T, id string) (*T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := m[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := cloneValue(v)
	return &cp, nil
}

// putRecord stores a deep copy of val into m under id.
func putRecord[T any](ctx context.Context, m map[string]T, id string, val *T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m[id] = cloneValue(*val)
	return nil
}

// deleteRecord removes key id from m or returns core.ErrNotFound when absent.
func deleteRecord[T any](ctx context.Context, m map[string]T, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := m[id]; !ok {
		return core.ErrNotFound
	}
	delete(m, id)
	return nil
}

// listWhere returns cloned records from m matching pred.
func listWhere[T any](ctx context.Context, m map[string]T, pred func(T) bool) ([]T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []T
	for _, v := range m {
		if pred(v) {
			out = append(out, cloneValue(v))
		}
	}
	return out, nil
}
