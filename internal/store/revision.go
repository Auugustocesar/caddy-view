package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotFound means a revision with the requested number does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalidRevision means a revision, or a status for one, is malformed.
var ErrInvalidRevision = errors.New("invalid revision")

const (
	// DefaultRevisionLimit is how many revisions Revisions returns when the query does not say.
	DefaultRevisionLimit = 50
	// MaxRevisionLimit caps the revisions Revisions returns in one call.
	MaxRevisionLimit = 1000
)

// RevisionQuery selects revisions, newest first. The zero value selects the newest DefaultRevisionLimit.
type RevisionQuery struct {
	// Limit is the maximum number of revisions. Zero means DefaultRevisionLimit, values above MaxRevisionLimit are
	// capped, and a negative value is ErrInvalidQuery.
	Limit int
	// Before, when positive, selects only revisions with a lower number, to page backwards.
	Before int64
}

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
	Time    time.Time
	Trigger RevisionTrigger
	Status  RevisionStatus
	Content string
	// SHA256 is the lowercase hex digest of Content.
	SHA256 string
	// Error is why the revision failed; empty otherwise.
	Error string
	// Sites is the managed part of the state this revision describes; nil for a revision made without managed sites.
	Sites *RevisionSites
}

func validTrigger(t RevisionTrigger) bool {
	switch t {
	case TriggerManualEdit, TriggerSiteChange, TriggerRollback:
		return true
	}
	return false
}

func validStatus(s RevisionStatus) bool {
	switch s {
	case StatusPending, StatusLoading, StatusApplied, StatusFailed, StatusRecoveryRequired:
		return true
	}
	return false
}

// validDigest reports whether s is a SHA-256 in lower-case hex.
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validateRevision checks a revision before it is stored.
func validateRevision(r Revision) error {
	switch {
	case !validTrigger(r.Trigger):
		return fmt.Errorf("%w: unknown trigger %q", ErrInvalidRevision, r.Trigger)
	case !validStatus(r.Status):
		return fmt.Errorf("%w: unknown status %q", ErrInvalidRevision, r.Status)
	case !validDigest(r.SHA256):
		return fmt.Errorf("%w: the digest is not 64 lower-case hex characters", ErrInvalidRevision)
	case r.Sites != nil && !validDigest(r.Sites.SHA256):
		return fmt.Errorf("%w: the digest of the managed file is not 64 lower-case hex characters", ErrInvalidRevision)
	case r.Time.IsZero():
		return fmt.Errorf("%w: no time", ErrInvalidRevision)
	}
	return nil
}

// normalizeRevision rounds the time to the millisecond in UTC, as the SQLite store keeps it, so both stores agree.
func normalizeRevision(r Revision) Revision {
	r.Time = time.UnixMilli(r.Time.UnixMilli()).UTC()
	r.Sites = cloneRevisionSites(r.Sites)
	return r
}

// revisionLimit applies the default and the cap to the limit of a query.
func revisionLimit(q RevisionQuery) (int, error) {
	switch {
	case q.Limit < 0:
		return 0, fmt.Errorf("%w: negative limit %d", ErrInvalidQuery, q.Limit)
	case q.Limit == 0:
		return DefaultRevisionLimit, nil
	case q.Limit > MaxRevisionLimit:
		return MaxRevisionLimit, nil
	}
	return q.Limit, nil
}
