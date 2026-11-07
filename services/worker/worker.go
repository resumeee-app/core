// Package worker provides an in-memory concurrent worker pool and execution pipeline
// for asynchronous resume generation and background batch synchronization.
//
// Key components include:
//   - Pool: Concurrent worker pool managing worker goroutines and channel queues
//   - Pipeline: Multi-stage resume generation pipeline coordinating prompt construction,
//     LLM invocation, anti-hallucination validation, and version persistence
//   - PDFParsePipeline: Asynchronous background worker for processing uploaded PDF resumes
package worker
