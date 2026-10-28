package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const siteColumns = "id, hostname, kind, upstreams, skip_tls_verify, tls, redirect_target, redirect_code, access_log, enabled, created_at, updated_at"

// Sites returns the managed sites, ordered by hostname.
func (s *SQLite) Sites(ctx context.Context) ([]Site, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+siteColumns+" FROM sites ORDER BY hostname, id")
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		var (
			site                     Site
			kind, tlsMode, upstreams string
			skip, accessLog, enabled bool
			createdAt, updatedAt     int64
		)
		err := rows.Scan(&site.ID, &site.Hostname, &kind, &upstreams, &skip, &tlsMode, &site.RedirectTarget, &site.RedirectCode,
			&accessLog, &enabled, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("list sites: %w", err)
		}
		if err := json.Unmarshal([]byte(upstreams), &site.Upstreams); err != nil {
			return nil, fmt.Errorf("list sites: the upstreams of %s: %w", site.Hostname, err)
		}
		site.Kind, site.TLS = SiteKind(kind), TLSMode(tlsMode)
		site.SkipTLSVerify, site.AccessLog, site.Enabled = skip, accessLog, enabled
		site.CreatedAt, site.UpdatedAt = time.UnixMilli(createdAt).UTC(), time.UnixMilli(updatedAt).UTC()
		out = append(out, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	return out, nil
}

// ReplaceSites replaces all the managed sites in one transaction. A malformed set is refused and nothing changes.
func (s *SQLite) ReplaceSites(ctx context.Context, all []Site) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSites(all); err != nil {
		return err
	}
	if s.closed.Load() {
		return ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace sites: %w", err)
	}
	defer tx.Rollback() // after a commit this does nothing
	if _, err := tx.ExecContext(ctx, "DELETE FROM sites"); err != nil {
		return fmt.Errorf("replace sites: %w", err)
	}
	for _, site := range all {
		if err := insertSite(ctx, tx, cloneSite(site)); err != nil {
			return fmt.Errorf("replace sites: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace sites: %w", err)
	}
	return nil
}

func insertSite(ctx context.Context, tx *sql.Tx, s Site) error {
	upstreams, err := json.Marshal(s.Upstreams)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO sites ("+siteColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		s.ID, s.Hostname, string(s.Kind), string(upstreams), s.SkipTLSVerify, string(s.TLS), s.RedirectTarget, s.RedirectCode,
		s.AccessLog, s.Enabled, s.CreatedAt.UnixMilli(), s.UpdatedAt.UnixMilli())
	return err
}
