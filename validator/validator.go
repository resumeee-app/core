// Package validator implements anti-hallucination provenance validation ("No Lies" invariant),
// verifying that all tailored achievements cite verified Master Profile facts and skills.
//
// It performs strict citation checks against master profile snapshots to ensure that
// generative AI tailoring runs never invent work experiences, bullet points, skills,
// or metrics not backed by candidate-verified career records.
package validator
