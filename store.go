package core

// Store aggregates all domain store interfaces into a unified persistence bundle.
type Store interface {
	Profiles() ProfileStore
	Career() CareerStore
	Branches() BranchStore
	Presets() PresetStore
	Templates() TemplateStore
	Users() UserStore
	Quotas() QuotaStore
	PDFJobs() PDFJobStore
	Close() error
}

// StoreBundle provides a concrete container for domain store instances and lifecycle hooks.
type StoreBundle struct {
	ProfileStore  ProfileStore
	CareerStore   CareerStore
	BranchStore   BranchStore
	PresetStore   PresetStore
	TemplateStore TemplateStore
	PDFJobStore   PDFJobStore
	QuotaStore    QuotaStore
	UserStore     UserStore
	Closer        func() error
}

var _ Store = (*StoreBundle)(nil)

// Profiles returns the underlying ProfileStore.
func (b *StoreBundle) Profiles() ProfileStore { return b.ProfileStore }

// Career returns the underlying CareerStore.
func (b *StoreBundle) Career() CareerStore { return b.CareerStore }

// Branches returns the underlying BranchStore.
func (b *StoreBundle) Branches() BranchStore { return b.BranchStore }

// Presets returns the underlying PresetStore.
func (b *StoreBundle) Presets() PresetStore { return b.PresetStore }

// Templates returns the underlying TemplateStore.
func (b *StoreBundle) Templates() TemplateStore { return b.TemplateStore }

// PDFJobs returns the underlying PDFJobStore.
func (b *StoreBundle) PDFJobs() PDFJobStore { return b.PDFJobStore }

// Quotas returns the underlying QuotaStore.
func (b *StoreBundle) Quotas() QuotaStore { return b.QuotaStore }

// Users returns the underlying UserStore.
func (b *StoreBundle) Users() UserStore { return b.UserStore }

// Close executes the configured Closer hook if present.
func (b *StoreBundle) Close() error {
	if b.Closer != nil {
		return b.Closer()
	}
	return nil
}
