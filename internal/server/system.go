package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/ingest"
	"github.com/Auugustocesar/caddy-view/internal/scrape"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// IngestStatus is what /api/system needs from the access log ingester.
type IngestStatus interface {
	Status() ingest.Status
}

// ScrapeStatus is what /api/system needs from the metrics scraper.
type ScrapeStatus interface {
	Status() scrape.Status
}

// DatabaseInfo describes the store behind the history.
type DatabaseInfo struct {
	// Kind is "sqlite" or "memory".
	Kind string
	// File is the database file; empty for memory.
	File string
	// SizeBytes is what the history holds.
	SizeBytes int64
	// SchemaVersion is the migration the database is at; zero for memory.
	SchemaVersion int
}

// Database is what /api/system needs from the store.
type Database interface {
	Info(ctx context.Context) (DatabaseInfo, error)
}

// Retention is how long the history is kept; zero means for ever, and for MaxBytes no limit.
type Retention struct {
	Raw      time.Duration
	Rollups  time.Duration
	MaxBytes int64
}

// ---- the answer

type systemAnswer struct {
	Caddy     caddyJSON      `json:"caddy"`
	Ingestion *ingestionJSON `json:"ingestion,omitempty"`
	Scraper   *scraperJSON   `json:"scraper,omitempty"`
	Database  map[string]any `json:"database,omitempty"`
	Retention *retentionJSON `json:"retention,omitempty"`
	Editing   editingJSON    `json:"editing"`
}

type caddyJSON struct {
	Reachable bool   `json:"reachable"`
	Endpoint  string `json:"endpoint"`
	Error     string `json:"error,omitempty"`
}

type ingestionJSON struct {
	State        string  `json:"state"`
	File         string  `json:"file"`
	Offset       int64   `json:"offset"`
	SkippedLines int64   `json:"skippedLines"`
	Gaps         int64   `json:"gaps"`
	NewestEntry  *string `json:"newestEntry"`
	Error        string  `json:"error,omitempty"`
}

type scraperJSON struct {
	State       string  `json:"state"`
	LastSuccess *string `json:"lastSuccess,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type retentionJSON struct {
	RawSeconds     int64 `json:"rawSeconds"`
	RollupsSeconds int64 `json:"rollupsSeconds"`
	MaxBytes       int64 `json:"maxBytes"`
}

type editingJSON struct {
	Enabled   bool       `json:"enabled"`
	Caddyfile string     `json:"caddyfile,omitempty"`
	Drift     *driftJSON `json:"drift,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type driftJSON struct {
	Known    bool   `json:"known"`
	Drifted  bool   `json:"drifted"`
	Revision int64  `json:"revision"`
	Running  string `json:"running"`
}

func utc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// system answers what the Config tab and the editing warnings need. It is a status page: it answers 200 whatever it
// finds, and a section that cannot be read says so itself, with the cause in the log and not in the answer.
func (a *api) system(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := systemAnswer{Caddy: caddyJSON{Reachable: true, Endpoint: a.opts.Endpoint}}
	if _, err := a.caddy.Config(ctx); err != nil {
		a.log.Debug("caddy is not reachable", "error", err)
		out.Caddy.Reachable, out.Caddy.Error = false, err.Error()
	}

	if a.opts.Ingest != nil {
		st := a.opts.Ingest.Status()
		out.Ingestion = &ingestionJSON{
			State: string(st.State), File: st.Path, Offset: st.Offset, SkippedLines: st.SkippedLines, Gaps: st.Gaps, Error: st.Error,
		}
		if a.opts.History != nil {
			entries, err := a.opts.History.QueryLogs(ctx, store.LogQuery{Limit: 1})
			switch {
			case err != nil:
				a.log.Error("system status: cannot read the newest log entry", "error", err)
				out.Ingestion.Error = "the newest entry could not be read"
			case len(entries) > 0:
				newest := utc(entries[0].Time)
				out.Ingestion.NewestEntry = &newest
			}
		}
	}

	if a.opts.Scrape != nil {
		st := a.opts.Scrape.Status()
		out.Scraper = &scraperJSON{State: string(st.State), Error: st.Error}
		if !st.LastSuccess.IsZero() {
			ok := utc(st.LastSuccess)
			out.Scraper.LastSuccess = &ok
		}
	}

	if a.opts.Database != nil {
		info, err := a.opts.Database.Info(ctx)
		if err != nil {
			a.log.Error("system status: cannot read the database", "error", err)
			out.Database = map[string]any{"error": "the database could not be read"}
		} else {
			out.Database = map[string]any{"kind": info.Kind, "sizeBytes": info.SizeBytes}
			if info.File != "" {
				out.Database["file"] = info.File
			}
			if info.SchemaVersion != 0 {
				out.Database["schemaVersion"] = info.SchemaVersion
			}
		}
	}

	if p := a.opts.Retention; p != nil {
		out.Retention = &retentionJSON{
			RawSeconds: int64(p.Raw / time.Second), RollupsSeconds: int64(p.Rollups / time.Second), MaxBytes: p.MaxBytes,
		}
	}

	if a.opts.Editor != nil {
		out.Editing = editingJSON{Enabled: true, Caddyfile: a.opts.CaddyfilePath}
		drift, err := a.opts.Editor.Reconcile(ctx)
		if err != nil {
			a.log.Error("system status: cannot compare the Caddyfile with its revisions", "error", err)
			out.Editing.Error = "the Caddyfile could not be compared with its revisions"
		} else {
			out.Editing.Drift = &driftJSON{Known: drift.Known, Drifted: drift.Drifted, Revision: drift.Revision, Running: runningName(drift.Running)}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func runningName(s configmgr.RunState) string {
	switch s {
	case configmgr.RunMatches:
		return "matches"
	case configmgr.RunDiffers:
		return "differs"
	}
	return "unknown"
}
