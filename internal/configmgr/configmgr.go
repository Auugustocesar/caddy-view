// Package configmgr changes the Caddyfile safely: every change is validated by Caddy, written atomically, loaded, and
// undone when Caddy refuses it. Each attempt is kept as a revision.
package configmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// Errors returned by the Manager. They are wrapped with the cause, so test them with errors.Is.
var (
	// ErrConflict means the Caddyfile changed since the caller read it; nothing was written or recorded.
	ErrConflict = errors.New("the Caddyfile changed since it was read")
	// ErrInvalid means Caddy rejected the candidate. The cause is the *caddy.AdaptError, with the line.
	ErrInvalid = errors.New("caddy rejected the Caddyfile")
	// ErrApplyFailed means Caddy refused to load the candidate and the previous configuration was restored.
	ErrApplyFailed = errors.New("caddy could not load the Caddyfile, the previous one was restored")
	// ErrRecoveryRequired means the candidate failed to load and the previous Caddyfile could not be restored or
	// reloaded either; the file and the running Caddy may disagree.
	ErrRecoveryRequired = errors.New("the previous Caddyfile could not be restored, manual recovery is required")
)

// Caddy is what the Manager needs from the Admin API client.
type Caddy interface {
	Adapt(ctx context.Context, caddyfile string) (*caddy.AdaptResult, error)
	Load(ctx context.Context, caddyfile string) error
}

// RevisionStore keeps the revisions.
type RevisionStore interface {
	// AddRevision stores r and returns it with its Number assigned.
	AddRevision(ctx context.Context, r store.Revision) (store.Revision, error)
	// UpdateRevision sets the status and error of a revision; store.ErrNotFound when it does not exist.
	UpdateRevision(ctx context.Context, number int64, status store.RevisionStatus, errMsg string) error
	// LatestApplied returns the newest revision with status applied, and false when there is none.
	LatestApplied(ctx context.Context) (store.Revision, bool, error)
	// Revision returns one revision; store.ErrNotFound when it does not exist.
	Revision(ctx context.Context, number int64) (store.Revision, error)
}

// Options configures a Manager.
type Options struct {
	// Path is the Caddyfile on disk.
	Path string
	// Now gives the current time. Zero means time.Now.
	Now func() time.Time
	// Logger receives operational logs. A nil Logger discards them.
	Logger *slog.Logger
}

// File is the Caddyfile as it is on disk.
type File struct {
	Content string
	// SHA256 is the lowercase hex digest of Content.
	SHA256 string
}

// ApplyRequest is a change to apply.
type ApplyRequest struct {
	// Content is the whole new Caddyfile.
	Content string
	// BaseSHA256 is the digest of the file the content was edited from.
	BaseSHA256 string
	// Trigger says why; the default is store.TriggerManualEdit.
	Trigger store.RevisionTrigger
}

// Drift compares the file on disk with the newest applied revision.
type Drift struct {
	// Known is false when there is no applied revision yet, so there is nothing to compare with.
	Known bool
	// Drifted is true when the file differs from the newest applied revision.
	Drifted bool
	// Revision is the number of the newest applied revision, when Known.
	Revision int64
	// DiskSHA256 is the digest of the file on disk.
	DiskSHA256 string
}

// restoreTimeout bounds the restore of the previous Caddyfile, which ignores the caller's cancellation.
const restoreTimeout = 30 * time.Second

// Manager applies Caddyfile changes one at a time.
type Manager struct {
	caddy Caddy
	store RevisionStore
	opts  Options
	log   *slog.Logger
	now   func() time.Time
	// mu serialises Apply and Rollback.
	mu sync.Mutex
}

// New returns a Manager.
func New(c Caddy, s RevisionStore, opts Options) *Manager {
	m := &Manager{caddy: c, store: s, opts: opts, log: opts.Logger, now: opts.Now}
	if m.log == nil {
		m.log = slog.New(slog.DiscardHandler)
	}
	if m.now == nil {
		m.now = time.Now
	}
	return m
}

func digest(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Current reads the Caddyfile from disk.
func (m *Manager) Current(context.Context) (File, error) {
	b, err := os.ReadFile(m.opts.Path)
	if err != nil {
		return File{}, fmt.Errorf("read the Caddyfile: %w", err)
	}
	content := string(b)
	return File{Content: content, SHA256: digest(content)}, nil
}

// Validate asks Caddy to adapt a candidate without applying it. A rejection is ErrInvalid wrapping the
// *caddy.AdaptError.
func (m *Manager) Validate(ctx context.Context, content string) (*caddy.AdaptResult, error) {
	res, err := m.caddy.Adapt(ctx, content)
	if err != nil {
		var adaptErr *caddy.AdaptError
		if errors.As(err, &adaptErr) {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return nil, fmt.Errorf("validate the Caddyfile: %w", err)
	}
	return res, nil
}

// Apply validates, writes and loads a candidate, restoring the previous Caddyfile when Caddy refuses it. The returned
// revision is the record of this attempt, also when the error is not nil, unless the attempt never started
// (ErrConflict, or the revision could not be stored).
func (m *Manager) Apply(ctx context.Context, req ApplyRequest) (store.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.apply(ctx, req)
}

// Rollback applies the content of an earlier revision as a new revision with the rollback trigger.
func (m *Manager) Rollback(ctx context.Context, number int64) (store.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.store.Revision(ctx, number)
	if err != nil {
		return store.Revision{}, fmt.Errorf("find revision %d: %w", number, err)
	}
	cur, err := m.Current(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	return m.apply(ctx, ApplyRequest{Content: old.Content, BaseSHA256: cur.SHA256, Trigger: store.TriggerRollback})
}

// Reconcile reports whether the Caddyfile on disk still is the newest applied revision.
func (m *Manager) Reconcile(ctx context.Context) (Drift, error) {
	cur, err := m.Current(ctx)
	if err != nil {
		return Drift{}, err
	}
	rev, ok, err := m.store.LatestApplied(ctx)
	if err != nil {
		return Drift{}, fmt.Errorf("find the newest applied revision: %w", err)
	}
	d := Drift{DiskSHA256: cur.SHA256}
	if ok {
		d.Known = true
		d.Revision = rev.Number
		d.Drifted = rev.SHA256 != cur.SHA256
	}
	return d, nil
}

// apply runs the apply flow of docs/PLAN.md section 6. The caller holds m.mu.
func (m *Manager) apply(ctx context.Context, req ApplyRequest) (store.Revision, error) {
	cur, err := m.Current(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	if cur.SHA256 != req.BaseSHA256 {
		return store.Revision{}, ErrConflict
	}
	trigger := req.Trigger
	if trigger == "" {
		trigger = store.TriggerManualEdit
	}
	rev, err := m.store.AddRevision(ctx, store.Revision{
		Time:    m.now().UnixMilli(),
		Trigger: trigger,
		Status:  store.StatusPending,
		Content: req.Content,
		SHA256:  digest(req.Content),
	})
	if err != nil {
		return store.Revision{}, fmt.Errorf("record the revision: %w", err)
	}

	if _, err := m.Validate(ctx, req.Content); err != nil {
		return m.finish(ctx, rev, store.StatusFailed, err)
	}
	if rev, err = m.setStatus(ctx, rev, store.StatusLoading, nil); err != nil {
		return m.finish(ctx, rev, store.StatusFailed, err)
	}
	if err := writeAtomic(m.opts.Path, req.Content); err != nil {
		return m.finish(ctx, rev, store.StatusFailed, err)
	}

	loadErr := m.caddy.Load(ctx, req.Content)
	if loadErr == nil {
		// The new Caddyfile is live; what is left is only the record of it.
		return m.finish(ctx, rev, store.StatusApplied, nil)
	}
	m.log.Warn("caddy refused the Caddyfile, restoring the previous one", "revision", rev.Number, "error", loadErr)
	if restoreErr := m.restore(ctx, cur.Content); restoreErr != nil {
		m.log.Error("the previous Caddyfile could not be restored", "revision", rev.Number, "error", restoreErr)
		return m.finish(ctx, rev, store.StatusRecoveryRequired,
			fmt.Errorf("%w: %w; restore: %w", ErrRecoveryRequired, loadErr, restoreErr))
	}
	return m.finish(ctx, rev, store.StatusFailed, fmt.Errorf("%w: %w", ErrApplyFailed, loadErr))
}

// restore puts the previous content back on disk and has Caddy load it. It ignores the cancellation of ctx, so a
// caller that gave up cannot leave the restore half done.
func (m *Manager) restore(ctx context.Context, previous string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
	defer cancel()
	if err := writeAtomic(m.opts.Path, previous); err != nil {
		return err
	}
	if err := m.caddy.Load(ctx, previous); err != nil {
		return fmt.Errorf("reload the previous Caddyfile: %w", err)
	}
	return nil
}

// setStatus records a state change and returns the revision as it now is. cause is the reason for a failure, or nil.
func (m *Manager) setStatus(ctx context.Context, rev store.Revision, status store.RevisionStatus, cause error) (store.Revision, error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if err := m.store.UpdateRevision(ctx, rev.Number, status, msg); err != nil {
		return rev, fmt.Errorf("record revision %d as %s: %w", rev.Number, status, err)
	}
	rev.Status, rev.Error = status, msg
	return rev, nil
}

// finish records the end state of an attempt and returns it with cause, the error of the attempt. The record is
// written even when the caller's context is done, because the attempt happened either way. A failure to write the
// record is logged and, when the attempt itself succeeded, returned.
func (m *Manager) finish(ctx context.Context, rev store.Revision, status store.RevisionStatus, cause error) (store.Revision, error) {
	rev, err := m.setStatus(context.WithoutCancel(ctx), rev, status, cause)
	if err != nil {
		m.log.Error("the outcome of a revision could not be recorded", "revision", rev.Number, "error", err)
		if cause == nil {
			return rev, err
		}
	}
	return rev, cause
}

// writeAtomic replaces the file with content through a temporary file in the same directory, keeping the file mode,
// so a crash never leaves a half-written Caddyfile.
func writeAtomic(path, content string) (err error) {
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("write the Caddyfile: %w", err)
	}
	defer func() {
		if err != nil {
			// The temporary file is garbage already; nothing more can be done if it cannot be removed.
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.WriteString(content); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("write the Caddyfile: %w", err)
	}
	return nil
}
