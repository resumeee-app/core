// Package profile implements career graph domain services, main profile management,
// and JSON Resume schema import/export interoperability.
//
// It serves as the primary manager of the Tier-1 Main Career Profile ("Source of Truth"),
// coordinating career facts, experiences, skills, projects, education, and certifications.
// It also provides bidirectional mapping between Resumeee's internal domain models
// and the open JSON Resume (v1.0.0) standard schema.
package profile
