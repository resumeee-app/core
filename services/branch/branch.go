// Package branch provides domain services for managing targeted resume branches,
// version lifecycles, iterative refinements, and asynchronous generation coordination.
//
// A branch represents a tailored variant of a candidate's main career profile
// targeted at a specific job description, company, or target role. Each branch
// maintains an immutable history of generated resume versions.
//
// Key capabilities include:
//   - Branch lifecycle management (Create, Get, List, Update, Archive)
//   - Asynchronous generation dispatch to the worker pool
//   - Version creation, inspection, and refinement iteration loops
//   - Synchronization with upstream Main Profile updates
//   - Live template rendering across HTML, Markdown, and Plain Text formats
package branch
