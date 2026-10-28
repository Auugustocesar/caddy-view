package configmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/caddyfile"
	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// Errors of the managed sites. They are wrapped with the cause, so test them with errors.Is.
var (
	// ErrNoManagedFile means no managed file is configured, so there is nowhere to write sites.
	ErrNoManagedFile = errors.New("no managed file is configured")
	// ErrInvalidSite means a site is not valid. The cause is an *InvalidSitesError, with every problem.
	ErrInvalidSite = errors.New("invalid site")
	// ErrNotImported means the Caddyfile does not import the managed file, so a site written there would never be loaded.
	ErrNotImported = errors.New("the Caddyfile does not import the managed file")
	// ErrManagedEdited means the managed file was changed outside caddy-view, and writing it would lose that change.
	ErrManagedEdited = errors.New("the managed file was edited outside caddy-view")
	// ErrSiteRejected means Caddy rejected the sites, and the cause is a *SiteError that says which one.
	ErrSiteRejected = errors.New("caddy rejected a managed site")
)

// SiteFieldError is a problem with one field of one site.
type SiteFieldError struct {
	// SiteID and Hostname say which site; Field is the JSON name of the field.
	SiteID, Hostname, Field, Message string
}

// InvalidSitesError lists every problem found in a set of sites.
type InvalidSitesError struct {
	Errors []SiteFieldError
}

func (e *InvalidSitesError) Error() string {
	if len(e.Errors) == 0 {
		return "invalid site"
	}
	first := e.Errors[0]
	return fmt.Sprintf("invalid site %s: %s: %s", first.Hostname, first.Field, first.Message)
}

// Is makes errors.Is(err, ErrInvalidSite) true for this error.
func (e *InvalidSitesError) Is(target error) bool { return target == ErrInvalidSite }

// SiteError is the cause of ErrSiteRejected: Caddy refused the managed file, and this is the site it concerns.
type SiteError struct {
	SiteID, Hostname string
	// Line is the line of the managed file Caddy named, or 0 when it named only the hostname.
	Line int
	// Message is what Caddy said.
	Message string
}

func (e *SiteError) Error() string {
	return fmt.Sprintf("caddy rejected the site %s: %s", e.Hostname, e.Message)
}

// Is makes errors.Is(err, ErrSiteRejected) true for this error.
func (e *SiteError) Is(target error) bool { return target == ErrSiteRejected }

// Sites returns the managed sites.
func (m *Manager) Sites(ctx context.Context) ([]store.Site, error) { return m.store.Sites(ctx) }

// ---- the managed file

// managedFile is the managed file as it is on disk.
type managedFile struct {
	content string
	existed bool
}

func (m *Manager) readManaged() (managedFile, error) {
	b, err := os.ReadFile(m.opts.ManagedPath)
	if errors.Is(err, os.ErrNotExist) {
		return managedFile{}, nil
	}
	if err != nil {
		return managedFile{}, fmt.Errorf("read the managed file: %w", err)
	}
	return managedFile{content: string(b), existed: true}, nil
}

func (m *Manager) writeManaged(content string) error {
	if err := os.MkdirAll(filepath.Dir(m.opts.ManagedPath), 0o755); err != nil {
		return fmt.Errorf("write the managed file: %w", err)
	}
	return writeAtomic(m.opts.ManagedPath, content)
}

// restoreManaged puts the managed file back as it was: its old content, or no file if there was none.
func (m *Manager) restoreManaged(prev managedFile) error {
	if prev.existed {
		return writeAtomic(m.opts.ManagedPath, prev.content)
	}
	if err := os.Remove(m.opts.ManagedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the managed file: %w", err)
	}
	return nil
}

// carried is the managed state in force: that of the newest applied revision that had one, or nil. A revision that
// changes only the Caddyfile carries it along, so that every revision describes the whole state.
func (m *Manager) carried(ctx context.Context) (*store.RevisionSites, error) {
	if m.opts.ManagedPath == "" {
		return nil, nil
	}
	rev, ok, err := m.store.LatestApplied(ctx)
	if err != nil {
		return nil, fmt.Errorf("find the managed state in force: %w", err)
	}
	if !ok || rev.Sites == nil {
		return nil, nil
	}
	return rev.Sites, nil
}

// importsManaged reports whether the Caddyfile text imports the managed file.
func (m *Manager) importsManaged(caddyfileText string) bool {
	return caddyfile.ImportsFile(caddyfileText, filepath.ToSlash(filepath.Dir(m.opts.Path)), filepath.ToSlash(m.opts.ManagedPath))
}

// checkManagedBase refuses to overwrite a managed file that caddy-view did not write: it may only be missing, empty,
// or the text of the last applied managed state.
func (m *Manager) checkManagedBase(ctx context.Context) (managedFile, error) {
	file, err := m.readManaged()
	if err != nil {
		return managedFile{}, err
	}
	if strings.TrimSpace(file.content) == "" {
		return file, nil
	}
	carried, err := m.carried(ctx)
	if err != nil {
		return managedFile{}, err
	}
	if carried == nil || digest(file.content) != carried.SHA256 {
		return managedFile{}, fmt.Errorf("%w: %s holds something caddy-view did not write; move it elsewhere or empty the file", ErrManagedEdited, m.opts.ManagedPath)
	}
	return file, nil
}

// ---- preparing the sites

func (m *Manager) siteOptions(ctx context.Context) sites.Options {
	if m.opts.AccessLog == nil {
		return sites.Options{}
	}
	return sites.Options{AccessLog: m.opts.AccessLog(ctx)}
}

// prepare normalises and validates the desired sites, and returns them with the managed file they produce.
func (m *Manager) prepare(ctx context.Context, desired []store.Site) ([]store.Site, sites.Output, error) {
	opts := m.siteOptions(ctx)
	rows := make([]store.Site, len(desired))
	for i, d := range desired {
		rows[i] = sites.Normalize(d)
	}
	var problems []SiteFieldError
	for i, row := range rows {
		others := slices.Concat(rows[:i], rows[i+1:])
		for _, p := range sites.Validate(row, others, opts) {
			problems = append(problems, SiteFieldError{SiteID: row.ID, Hostname: row.Hostname, Field: p.Field, Message: p.Message})
		}
	}
	if len(problems) > 0 {
		return nil, sites.Output{}, &InvalidSitesError{Errors: problems}
	}
	return rows, sites.Generate(rows, opts), nil
}

// stamp gives the rows their times: a site that was there keeps its creation time, and its update time moves only when
// it changed; a new site is created and updated now.
func (m *Manager) stamp(ctx context.Context, rows []store.Site) error {
	existing, err := m.store.Sites(ctx)
	if err != nil {
		return fmt.Errorf("read the managed sites: %w", err)
	}
	byID := make(map[string]store.Site, len(existing))
	for _, e := range existing {
		byID[e.ID] = e
	}
	now := m.now()
	for i := range rows {
		old, found := byID[rows[i].ID]
		switch {
		case !found:
			rows[i].CreatedAt, rows[i].UpdatedAt = now, now
		case sameSiteFields(old, rows[i]):
			rows[i].CreatedAt, rows[i].UpdatedAt = old.CreatedAt, old.UpdatedAt
		default:
			rows[i].CreatedAt, rows[i].UpdatedAt = old.CreatedAt, now
		}
	}
	return nil
}

// sameSiteFields compares what the user can change, not the times.
func sameSiteFields(a, b store.Site) bool {
	a.CreatedAt, a.UpdatedAt, b.CreatedAt, b.UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if len(a.Upstreams) == 0 && len(b.Upstreams) == 0 {
		a.Upstreams, b.Upstreams = nil, nil
	}
	return reflect.DeepEqual(a, b)
}

// PreviewSites validates the desired sites and returns the managed file that would be written, touching nothing.
func (m *Manager) PreviewSites(ctx context.Context, desired []store.Site) (string, error) {
	if m.opts.ManagedPath == "" {
		return "", ErrNoManagedFile
	}
	_, out, err := m.prepare(ctx, desired)
	if err != nil {
		return "", err
	}
	return out.Text, nil
}

// ApplySites makes the desired set of sites the managed sites: it generates the managed file, writes it, has Caddy load
// the Caddyfile that imports it, verifies what runs, and only then saves the rows. When anything fails the managed file
// is put back and the rows are left as they were.
func (m *Manager) ApplySites(ctx context.Context, desired []store.Site) (store.Revision, error) {
	if m.opts.ManagedPath == "" {
		return store.Revision{}, ErrNoManagedFile
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rows, out, err := m.prepare(ctx, desired)
	if err != nil {
		return store.Revision{}, err
	}
	cur, err := m.Current(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	if imports := caddyfile.RelativeImports(cur.Content); len(imports) > 0 {
		return store.Revision{}, &RelativeImportError{Pattern: imports[0].Pattern, Line: imports[0].Line}
	}
	if !m.importsManaged(cur.Content) {
		return store.Revision{}, fmt.Errorf("%w: add `import %s` to the Caddyfile", ErrNotImported, filepath.ToSlash(m.opts.ManagedPath))
	}
	prev, err := m.checkManagedBase(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	if err := m.stamp(ctx, rows); err != nil {
		return store.Revision{}, err
	}
	return m.applyManaged(ctx, plan{trigger: store.TriggerSiteChange, main: cur.Content, rows: rows, out: out}, cur, prev)
}

// rollbackManaged restores a revision that has managed state: the Caddyfile, the managed file as it was, and the rows.
func (m *Manager) rollbackManaged(ctx context.Context, old store.Revision, cur File) (store.Revision, error) {
	main := old.Content
	if m.opts.SitesOnly {
		main = cur.Content // the Caddyfile is the person's: only the managed state goes back
	}
	if imports := caddyfile.RelativeImports(main); len(imports) > 0 {
		return store.Revision{}, &RelativeImportError{Pattern: imports[0].Pattern, Line: imports[0].Line}
	}
	prev, err := m.checkManagedBase(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	rows := slices.Clone(old.Sites.Snapshot)
	// The text is restored as it was, not generated again; the blocks only serve to name a site if Caddy complains,
	// and are kept when generating the same rows gives the same text.
	out := sites.Output{Text: old.Sites.Content}
	if again := sites.Generate(rows, m.siteOptions(ctx)); again.Text == old.Sites.Content {
		out = again
	}
	return m.applyManaged(ctx, plan{trigger: store.TriggerRollback, main: main, rows: rows, out: out}, cur, prev)
}

// plan is a change of the managed state: the managed file and rows to have, and the Caddyfile text to have, which is
// the one on disk unless a rollback restores another.
type plan struct {
	trigger store.RevisionTrigger
	main    string
	rows    []store.Site
	out     sites.Output
}

// applyManaged writes the managed file (and the Caddyfile when it changes), has Caddy load the Caddyfile, verifies
// what runs, and saves the rows. The caller holds m.mu and has checked what can be checked without a revision.
func (m *Manager) applyManaged(ctx context.Context, p plan, cur File, prev managedFile) (store.Revision, error) {
	text := p.out.Text
	// The sites-only mode keeps no copy of the Caddyfile: it may hold tokens and passwords.
	kept := p.main
	if m.opts.SitesOnly {
		kept = ""
	}
	rev, err := m.store.AddRevision(ctx, store.Revision{
		Time: m.now(), Trigger: p.trigger, Status: store.StatusPending, Content: kept, SHA256: digest(kept),
		Sites: &store.RevisionSites{Content: text, SHA256: digest(text), Snapshot: p.rows},
	})
	if err != nil {
		return store.Revision{}, fmt.Errorf("record the revision: %w", err)
	}

	// The managed text on its own, before anything is written.
	if _, err := m.caddy.Adapt(ctx, text); err != nil {
		return m.finish(ctx, rev, store.StatusFailed, m.siteProblem(err, p, true))
	}
	if rev, err = m.setStatus(ctx, rev, store.StatusLoading, nil); err != nil {
		return m.finish(ctx, rev, store.StatusFailed, err)
	}

	mainChanged := p.main != cur.Content
	// undo puts the files back as they were; Caddy has not been told anything yet, or is told again by the caller.
	undo := func() error {
		err := m.restoreManaged(prev)
		if mainChanged {
			err = errors.Join(err, writeAtomic(m.opts.Path, cur.Content))
		}
		return err
	}
	if err := m.writeManaged(text); err != nil {
		return m.finish(ctx, rev, store.StatusFailed, errors.Join(err, undo()))
	}
	if mainChanged {
		if err := writeAtomic(m.opts.Path, p.main); err != nil {
			return m.finish(ctx, rev, store.StatusFailed, errors.Join(err, undo()))
		}
	}

	// Now that Caddy can see the new sites, it checks the whole.
	adapted, err := m.caddy.Adapt(ctx, p.main)
	if err == nil {
		err = m.checkAdmin(ctx, adapted.Config)
	}
	if err != nil {
		if undoErr := undo(); undoErr != nil {
			return m.finish(ctx, rev, store.StatusRecoveryRequired, fmt.Errorf("%w: %w; restore: %w", ErrRecoveryRequired, err, undoErr))
		}
		return m.finish(ctx, rev, store.StatusFailed, m.siteProblem(err, p, false))
	}

	loadErr := m.caddy.Load(ctx, p.main)
	if loadErr == nil {
		loadErr = m.verify(ctx, adapted.Config)
	}
	if loadErr != nil {
		m.log.Warn("caddy refused the managed sites, restoring the previous state", "revision", rev.Number, "error", loadErr)
		restoreErr := undo()
		if restoreErr == nil {
			restoreErr = m.reload(ctx, cur.Content)
		}
		if restoreErr != nil {
			m.log.Error("the previous state could not be restored", "revision", rev.Number, "error", restoreErr)
			return m.finish(ctx, rev, store.StatusRecoveryRequired, fmt.Errorf("%w: %w; restore: %w", ErrRecoveryRequired, loadErr, restoreErr))
		}
		return m.finish(ctx, rev, store.StatusFailed, fmt.Errorf("%w: %w", ErrApplyFailed, loadErr))
	}

	// Caddy runs it. The rows follow, so that the table describes what runs; if they cannot be saved, that is for a person.
	if err := m.store.ReplaceSites(ctx, p.rows); err != nil {
		return m.finish(ctx, rev, store.StatusRecoveryRequired, fmt.Errorf("%w: it is applied but the sites could not be saved: %w", ErrRecoveryRequired, err))
	}
	return m.finish(ctx, rev, store.StatusApplied, nil)
}

// reload has Caddy load the Caddyfile text again, ignoring the cancellation of ctx like restore does.
func (m *Manager) reload(ctx context.Context, caddyfileText string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
	defer cancel()
	if err := m.caddy.Load(ctx, caddyfileText); err != nil {
		return fmt.Errorf("reload the previous Caddyfile: %w", err)
	}
	return nil
}

// ---- naming the site Caddy complains about

// siteProblem turns an error of Caddy into one that names the site, when it can tell which. fragment says the text
// that was adapted was the managed text alone, so a line number is a line of it; otherwise it was the Caddyfile, and
// only a line of the managed file named in the message counts.
func (m *Manager) siteProblem(err error, p plan, fragment bool) error {
	var adaptErr *caddy.AdaptError
	if !errors.As(err, &adaptErr) {
		return err
	}
	line := 0
	if fragment {
		line = adaptErr.Line
	} else {
		line = m.managedLine(adaptErr.Message)
	}
	if id, ok := p.out.SiteAt(line); ok {
		for _, row := range p.rows {
			if row.ID == id {
				return &SiteError{SiteID: id, Hostname: row.Hostname, Line: line, Message: adaptErr.Message}
			}
		}
	}
	for _, row := range p.rows {
		if row.Enabled && mentionsHost(adaptErr.Message, row.Hostname) {
			return &SiteError{SiteID: row.ID, Hostname: row.Hostname, Message: adaptErr.Message}
		}
	}
	return fmt.Errorf("%w: %w", ErrInvalid, err)
}

// managedLine finds "<managed file>:<line>" in a message of Caddy, and returns the line, or 0.
func (m *Manager) managedLine(message string) int {
	for _, name := range []string{m.opts.ManagedPath, filepath.ToSlash(m.opts.ManagedPath)} {
		re := regexp.MustCompile(regexp.QuoteMeta(name) + `:(\d+)`)
		if match := re.FindStringSubmatch(message); match != nil {
			n, _ := strconv.Atoi(match[1])
			return n
		}
	}
	return 0
}

// mentionsHost reports whether the message names the hostname as a whole word, not as part of a longer one.
func mentionsHost(message, host string) bool {
	re := regexp.MustCompile(`(^|[^a-z0-9.-])` + regexp.QuoteMeta(strings.ToLower(host)) + `($|[^a-z0-9-]|\.($|[^a-z0-9-]))`)
	return re.MatchString(strings.ToLower(message))
}
