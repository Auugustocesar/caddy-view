package store

import "errors"

// ErrNotFound means a revision with the requested number does not exist.
var ErrNotFound = errors.New("not found")

// RevisionTrigger says why a revision was created.
type RevisionTrigger string

// The reasons a revision is created.
const (
	TriggerManualEdit RevisionTrigger = "manual-edit"
	TriggerSiteChange RevisionTrigger = "site-change"
	TriggerRollback   RevisionTrigger = "rollback"
)

// RevisionStatus is where a revision is in the apply flow.
type RevisionStatus string

// The states of a revision: pending, loading, then applied, failed or recovery-required.
const (
	StatusPending          RevisionStatus = "pending"
	StatusLoading          RevisionStatus = "loading"
	StatusApplied          RevisionStatus = "applied"
	StatusFailed           RevisionStatus = "failed"
	StatusRecoveryRequired RevisionStatus = "recovery-required"
)

// Revision is one attempt to change the Caddyfile, with the full content that was tried.
type Revision struct {
	// Number is assigned by the store and strictly increases.
	Number  int64
	Time    int64 // Unix ms UTC
	Trigger RevisionTrigger
	Status  RevisionStatus
	Content string
	// SHA256 is the lowercase hex digest of Content.
	SHA256 string
	// Error is why the revision failed; empty otherwise.
	Error string
}
