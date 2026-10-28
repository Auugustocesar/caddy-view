package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrInvalidSite means a managed site is malformed in a way the store itself cannot accept: a missing or repeated
// ID or hostname, or an unknown kind or TLS mode. Whether a site makes sense is the business of the sites package.
var ErrInvalidSite = errors.New("invalid site")

// SiteKind is what a managed site does.
type SiteKind string

// The kinds of managed site.
const (
	SiteProxy    SiteKind = "proxy"
	SiteRedirect SiteKind = "redirect"
)

// TLSMode is how a managed site gets its certificate.
type TLSMode string

// The TLS modes of a managed site.
const (
	TLSAutomatic TLSMode = "automatic"
	TLSInternal  TLSMode = "internal"
	TLSOff       TLSMode = "off"
)

// Site is a site managed by caddy-view: what the user asked for. The managed Caddyfile is generated from these.
type Site struct {
	// ID identifies the site for as long as it lives; the hostname may change.
	ID       string
	Hostname string
	Kind     SiteKind
	// Upstreams are the backends of a proxy site, in the order they were given.
	Upstreams      []string
	SkipTLSVerify  bool
	TLS            TLSMode
	RedirectTarget string
	RedirectCode   int
	AccessLog      bool
	// BasicAuthUser and BasicAuthHash ask for a username and a password before the site answers. The hash is bcrypt; the
	// password itself is never stored. Both are empty, or neither.
	BasicAuthUser string
	BasicAuthHash string
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RevisionSites is the managed part of a revision: the text of the managed file and the rows it was generated from.
type RevisionSites struct {
	Content string
	// SHA256 is the lowercase hex digest of Content.
	SHA256   string
	Snapshot []Site
}

// validateSites checks a whole set of sites before anything is stored, so that a bad one rejects the set.
func validateSites(all []Site) error {
	ids := make(map[string]bool, len(all))
	hosts := make(map[string]bool, len(all))
	for i, s := range all {
		switch {
		case s.ID == "":
			return fmt.Errorf("%w: site %d has no ID", ErrInvalidSite, i)
		case s.Hostname == "":
			return fmt.Errorf("%w: site %s has no hostname", ErrInvalidSite, s.ID)
		case s.Kind != SiteProxy && s.Kind != SiteRedirect:
			return fmt.Errorf("%w: site %s has the unknown kind %q", ErrInvalidSite, s.ID, s.Kind)
		case s.TLS != TLSAutomatic && s.TLS != TLSInternal && s.TLS != TLSOff:
			return fmt.Errorf("%w: site %s has the unknown TLS mode %q", ErrInvalidSite, s.ID, s.TLS)
		case (s.BasicAuthUser == "") != (s.BasicAuthHash == ""):
			return fmt.Errorf("%w: site %s has half of a password: a user and a hash go together", ErrInvalidSite, s.ID)
		case ids[s.ID]:
			return fmt.Errorf("%w: the ID %s is used twice", ErrInvalidSite, s.ID)
		case hosts[strings.ToLower(s.Hostname)]:
			return fmt.Errorf("%w: the hostname %s is used twice", ErrInvalidSite, s.Hostname)
		}
		ids[s.ID], hosts[strings.ToLower(s.Hostname)] = true, true
	}
	return nil
}

// cloneSite copies a site so that nothing is shared with the caller, and rounds its times to the millisecond in UTC,
// as the SQLite store keeps them, so both stores agree. A nil list of upstreams stays nil.
func cloneSite(s Site) Site {
	if s.Upstreams != nil {
		s.Upstreams = append([]string{}, s.Upstreams...)
	}
	s.CreatedAt = time.UnixMilli(s.CreatedAt.UnixMilli()).UTC()
	s.UpdatedAt = time.UnixMilli(s.UpdatedAt.UnixMilli()).UTC()
	return s
}

func cloneSites(all []Site) []Site {
	out := make([]Site, len(all))
	for i, s := range all {
		out[i] = cloneSite(s)
	}
	return out
}

// sortedSites returns a copy of the sites ordered by hostname, ignoring case.
func sortedSites(all []Site) []Site {
	out := cloneSites(all)
	slices.SortFunc(out, func(a, b Site) int {
		if c := strings.Compare(strings.ToLower(a.Hostname), strings.ToLower(b.Hostname)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func cloneRevisionSites(r *RevisionSites) *RevisionSites {
	if r == nil {
		return nil
	}
	c := *r
	if r.Snapshot != nil {
		c.Snapshot = cloneSites(r.Snapshot)
	}
	return &c
}

// cloneRevision copies a revision, with its managed state, so that nothing is shared with the caller.
func cloneRevision(r Revision) Revision {
	r.Sites = cloneRevisionSites(r.Sites)
	return r
}

// Sites returns the managed sites, ordered by hostname.
func (m *Memory) Sites(ctx context.Context) ([]Site, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	return sortedSites(m.sites), nil
}

// ReplaceSites replaces all the managed sites. A malformed set is refused and nothing changes.
func (m *Memory) ReplaceSites(ctx context.Context, all []Site) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSites(all); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.sites = cloneSites(all)
	return nil
}
