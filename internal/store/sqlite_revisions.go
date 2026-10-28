package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const revisionColumns = "number, ts, cause, status, content, sha256, error, sites_content, sites_sha256, sites_snapshot"

// AddRevision stores r with the next number.
func (s *SQLite) AddRevision(ctx context.Context, r Revision) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	if err := validateRevision(r); err != nil {
		return Revision{}, err
	}
	if s.closed.Load() {
		return Revision{}, ErrClosed
	}
	r = normalizeRevision(r)
	var sitesContent, sitesSHA, sitesSnapshot any // nil, which is NULL, when there is no managed state
	if r.Sites != nil {
		snapshot, err := json.Marshal(r.Sites.Snapshot)
		if err != nil {
			return Revision{}, fmt.Errorf("store revision: %w", err)
		}
		sitesContent, sitesSHA, sitesSnapshot = r.Sites.Content, r.Sites.SHA256, string(snapshot)
	}
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO config_revisions (ts, cause, status, content, sha256, error, sites_content, sites_sha256, sites_snapshot) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.Time.UnixMilli(), string(r.Trigger), string(r.Status), r.Content, r.SHA256, r.Error, sitesContent, sitesSHA, sitesSnapshot)
	if err != nil {
		return Revision{}, fmt.Errorf("store revision: %w", err)
	}
	if r.Number, err = res.LastInsertId(); err != nil {
		return Revision{}, fmt.Errorf("store revision: %w", err)
	}
	return r, nil
}

// UpdateRevision sets the status and the error of a revision.
func (s *SQLite) UpdateRevision(ctx context.Context, number int64, status RevisionStatus, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validStatus(status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidRevision, status)
	}
	if s.closed.Load() {
		return ErrClosed
	}
	res, err := s.db.ExecContext(ctx, "UPDATE config_revisions SET status = ?, error = ? WHERE number = ?",
		string(status), errMsg, number)
	if err != nil {
		return fmt.Errorf("update revision %d: %w", number, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update revision %d: %w", number, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// LatestApplied returns the applied revision with the highest number.
func (s *SQLite) LatestApplied(ctx context.Context) (Revision, bool, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, false, err
	}
	if s.closed.Load() {
		return Revision{}, false, ErrClosed
	}
	r, err := scanRevision(s.db.QueryRowContext(ctx,
		"SELECT "+revisionColumns+" FROM config_revisions WHERE status = ? ORDER BY number DESC LIMIT 1", string(StatusApplied)))
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, false, nil
	}
	if err != nil {
		return Revision{}, false, fmt.Errorf("find the newest applied revision: %w", err)
	}
	return r, true, nil
}

// Revision returns one revision.
func (s *SQLite) Revision(ctx context.Context, number int64) (Revision, error) {
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	if s.closed.Load() {
		return Revision{}, ErrClosed
	}
	r, err := scanRevision(s.db.QueryRowContext(ctx, "SELECT "+revisionColumns+" FROM config_revisions WHERE number = ?", number))
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("read revision %d: %w", number, err)
	}
	return r, nil
}

// Revisions returns the revisions that match q, newest first.
func (s *SQLite) Revisions(ctx context.Context, q RevisionQuery) ([]Revision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := revisionLimit(q)
	if err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	query := "SELECT " + revisionColumns + " FROM config_revisions"
	var args []any
	if q.Before > 0 {
		query += " WHERE number < ?"
		args = append(args, q.Before)
	}
	query += " ORDER BY number DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("list revisions: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	return out, nil
}

// scanRevision reads one revision from a row.
func scanRevision(row interface{ Scan(dest ...any) error }) (Revision, error) {
	var (
		r       Revision
		ts      int64
		trigger string
		status  string
		// NULL means the revision has no managed state.
		sitesContent, sitesSHA, sitesSnapshot sql.NullString
	)
	if err := row.Scan(&r.Number, &ts, &trigger, &status, &r.Content, &r.SHA256, &r.Error, &sitesContent, &sitesSHA, &sitesSnapshot); err != nil {
		return Revision{}, err
	}
	r.Time = time.UnixMilli(ts).UTC()
	r.Trigger, r.Status = RevisionTrigger(trigger), RevisionStatus(status)
	if sitesContent.Valid {
		r.Sites = &RevisionSites{Content: sitesContent.String, SHA256: sitesSHA.String}
		if sitesSnapshot.Valid {
			if err := json.Unmarshal([]byte(sitesSnapshot.String), &r.Sites.Snapshot); err != nil {
				return Revision{}, fmt.Errorf("the managed sites of revision %d: %w", r.Number, err)
			}
		}
	}
	return r, nil
}

// Unfinished returns the revisions still pending or loading, oldest first.
func (s *SQLite) Unfinished(ctx context.Context) ([]Revision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+revisionColumns+
		" FROM config_revisions WHERE status IN (?, ?) ORDER BY number ASC", string(StatusPending), string(StatusLoading))
	if err != nil {
		return nil, fmt.Errorf("list unfinished revisions: %w", err)
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("list unfinished revisions: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list unfinished revisions: %w", err)
	}
	return out, nil
}
