package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // the pure Go driver, registered as "sqlite"
)

// LatestSchemaVersion is the number of the last migration this version of caddy-view knows.
const LatestSchemaVersion = 6

//go:embed migrations/*.sql
var migrationFiles embed.FS

// SQLite is a store backed by a SQLite database file.
type SQLite struct {
	db     *sql.DB
	closed atomic.Bool
}

// OpenSQLite opens the database at path, creating the file and its directory if needed, and brings the schema up
// to date. The directory is created with mode 0700 and the file with 0600.
func OpenSQLite(ctx context.Context, path string) (*SQLite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := createPrivateFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close() // the migration error is the one worth reporting
		return nil, fmt.Errorf("prepare database %s: %w", path, err)
	}
	return &SQLite{db: db}, nil
}

// createPrivateFile makes the directory and an empty database file with private permissions, unless they exist.
// SQLite gives its WAL and shared-memory files the mode of the database file.
func createPrivateFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	return f.Close()
}

// dsn builds the connection string of the history database.
func dsn(path string) string {
	return SQLiteURI(path, []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)"}, "immediate")
}

// SQLiteURI builds the connection string that opens the SQLite database file at path. The pragmas are applied to every
// connection the pool opens, and txlock, when not empty, is the kind of transaction `BEGIN` starts. It exists so that
// every caller turns a path into a URI the same way, including on Windows.
func SQLiteURI(path string, pragmas []string, txlock string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		p = "/" + p // file:///C:/dir/file.db
	}
	q := url.Values{}
	for _, pragma := range pragmas {
		q.Add("_pragma", pragma)
	}
	if txlock != "" {
		q.Set("_txlock", txlock)
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: q.Encode()}
	return u.String()
}

type migration struct {
	version int
	sql     string
}

// loadMigrations reads the embedded files, named NNNN_description.sql, in version order.
func loadMigrations() ([]migration, error) {
	files, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, f := range files {
		num, _, _ := strings.Cut(f.Name(), "_")
		version, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %q does not start with a number", f.Name())
		}
		body, err := fs.ReadFile(migrationFiles, "migrations/"+f.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations are not numbered 1..n: found %d at position %d", m.version, i+1)
		}
	}
	if len(out) != LatestSchemaVersion {
		return nil, fmt.Errorf("found %d migrations, LatestSchemaVersion is %d", len(out), LatestSchemaVersion)
	}
	return out, nil
}

// migrate applies the migrations the database has not seen. Each runs in its own immediate transaction that
// re-reads the version, so two processes opening the same new database apply it once.
func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	version, err := userVersion(ctx, db)
	if err != nil {
		return err
	}
	if version > LatestSchemaVersion {
		return fmt.Errorf("%w (database %d, this version %d)", ErrSchemaTooNew, version, LatestSchemaVersion)
	}
	for _, m := range migrations[version:] {
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // after a successful Commit this does nothing
	var current int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	if current >= m.version {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	// PRAGMA does not take bound parameters; the version is an integer from our own file names.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// SchemaVersion returns the number of the last migration applied to the database.
func (s *SQLite) SchemaVersion(ctx context.Context) (int, error) {
	if s.closed.Load() {
		return 0, ErrClosed
	}
	return userVersion(ctx, s.db)
}

// AppendLog stores the entries and the cursor atomically.
func (s *SQLite) AppendLog(ctx context.Context, batch LogBatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateBatch(batch); err != nil {
		return err
	}
	if s.closed.Load() {
		return ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("append log: %w", err)
	}
	defer tx.Rollback() // after a successful Commit this does nothing

	if len(batch.Entries) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO access_log_entries
			(ts, host, method, path, status, duration_us, bytes, client_ip) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("append log: %w", err)
		}
		defer stmt.Close()
		normalized := make([]LogEntry, len(batch.Entries))
		for i, e := range batch.Entries {
			e = normalize(e)
			normalized[i] = e
			if _, err := stmt.ExecContext(ctx, e.Time.UnixMilli(), e.Host, e.Method, e.Path, e.Status,
				e.Duration.Microseconds(), e.Bytes, e.ClientIP); err != nil {
				return fmt.Errorf("append log: %w", err)
			}
		}
		if err := addRollups(ctx, tx, normalized); err != nil {
			return fmt.Errorf("append log: %w", err)
		}
	}
	if c := batch.Cursor; c.Path != "" {
		_, err := tx.ExecContext(ctx, `INSERT INTO ingestion_cursors
			(path, file_id, fingerprint, byte_offset, skipped_lines, gaps) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET file_id = excluded.file_id, fingerprint = excluded.fingerprint,
				byte_offset = excluded.byte_offset, skipped_lines = excluded.skipped_lines, gaps = excluded.gaps`,
			c.Path, c.FileID, c.Fingerprint, c.Offset, c.SkippedLines, c.Gaps)
		if err != nil {
			return fmt.Errorf("save cursor: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("append log: %w", err)
	}
	return nil
}

// QueryLogs returns the entries that match q, newest first.
func (s *SQLite) QueryLogs(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := queryLimit(q)
	if err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}

	var where []string
	var args []any
	add := func(cond string, arg any) {
		where = append(where, cond)
		args = append(args, arg)
	}
	if q.BeforeID != 0 {
		add("id < ?", q.BeforeID)
	}
	if !q.From.IsZero() {
		add("ts >= ?", q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		add("ts < ?", q.To.UnixMilli())
	}
	if q.Host != "" {
		add("host = ?", q.Host)
	}
	if q.MinStatus != 0 {
		add("status >= ?", q.MinStatus)
	}
	if q.MaxStatus != 0 {
		add("status <= ?", q.MaxStatus)
	}
	if q.Text != "" {
		// instr is a literal search, unlike LIKE, which would treat % and _ as wildcards.
		add("instr(lower(path), ?) > 0", asciiLower(q.Text))
	}

	query := "SELECT id, ts, host, method, path, status, duration_us, bytes, client_ip FROM access_log_entries"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		var ts, durationUS int64
		if err := rows.Scan(&e.ID, &ts, &e.Host, &e.Method, &e.Path, &e.Status, &durationUS, &e.Bytes, &e.ClientIP); err != nil {
			return nil, fmt.Errorf("query logs: %w", err)
		}
		e.Time = time.UnixMilli(ts).UTC()
		e.Duration = time.Duration(durationUS) * time.Microsecond
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	return out, nil
}

// LoadCursor returns the saved cursor for path and whether there was one.
func (s *SQLite) LoadCursor(ctx context.Context, path string) (Cursor, bool, error) {
	if err := ctx.Err(); err != nil {
		return Cursor{}, false, err
	}
	if s.closed.Load() {
		return Cursor{}, false, ErrClosed
	}
	c := Cursor{Path: path}
	err := s.db.QueryRowContext(ctx, `SELECT file_id, fingerprint, byte_offset, skipped_lines, gaps
		FROM ingestion_cursors WHERE path = ?`, path).Scan(&c.FileID, &c.Fingerprint, &c.Offset, &c.SkippedLines, &c.Gaps)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, false, nil
	}
	if err != nil {
		return Cursor{}, false, fmt.Errorf("load cursor: %w", err)
	}
	return c, true, nil
}

// DeleteLogsBefore removes the entries older than t and returns how many it removed.
func (s *SQLite) DeleteLogsBefore(ctx context.Context, t time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.closed.Load() {
		return 0, ErrClosed
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM access_log_entries WHERE ts < ?", t.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("delete logs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete logs: %w", err)
	}
	return n, nil
}

// Close releases the database. It is safe to call more than once.
func (s *SQLite) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	return s.db.Close()
}

// AppendMetrics stores the samples atomically. A sample without a name or a time rejects the whole batch.
func (s *SQLite) AppendMetrics(ctx context.Context, samples []MetricSample) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMetrics(samples); err != nil {
		return err
	}
	if s.closed.Load() {
		return ErrClosed
	}
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("append metrics: %w", err)
	}
	defer tx.Rollback() // after a successful Commit this does nothing

	stmt, err := tx.PrepareContext(ctx, "INSERT INTO metric_samples (name, labels, ts, value, raw) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("append metrics: %w", err)
	}
	defer stmt.Close()
	for _, sample := range samples {
		if _, err := stmt.ExecContext(ctx, sample.Name, sample.Labels, sample.Time.UnixMilli(), sample.Value, sample.Raw); err != nil {
			return fmt.Errorf("append metrics: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("append metrics: %w", err)
	}
	return nil
}

// QueryMetrics returns the samples of one series selection, oldest first.
func (s *SQLite) QueryMetrics(ctx context.Context, q MetricQuery) ([]MetricSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := metricLimit(q)
	if err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}

	query := "SELECT name, labels, ts, value, raw FROM metric_samples WHERE name = ?"
	args := []any{q.Name}
	if q.Labels != "" {
		query += " AND labels = ?"
		args = append(args, q.Labels)
	}
	if !q.From.IsZero() {
		query += " AND ts >= ?"
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		query += " AND ts < ?"
		args = append(args, q.To.UnixMilli())
	}
	query += " ORDER BY ts, id LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()
	out := []MetricSample{}
	for rows.Next() {
		sample, err := scanMetric(rows)
		if err != nil {
			return nil, fmt.Errorf("query metrics: %w", err)
		}
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	return out, nil
}

// LastMetrics returns the most recently stored sample of every series, keyed by name followed by labels.
func (s *SQLite) LastMetrics(ctx context.Context) (map[string]MetricSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	// The distinct series come from the index alone; each one then looks up its newest row through the same index.
	rows, err := s.db.QueryContext(ctx, `SELECT m.name, m.labels, m.ts, m.value, m.raw
		FROM (SELECT DISTINCT name, labels FROM metric_samples) AS series
		JOIN metric_samples AS m ON m.id = (
			SELECT id FROM metric_samples WHERE name = series.name AND labels = series.labels
			ORDER BY ts DESC, id DESC LIMIT 1)`)
	if err != nil {
		return nil, fmt.Errorf("last metrics: %w", err)
	}
	defer rows.Close()
	out := make(map[string]MetricSample)
	for rows.Next() {
		sample, err := scanMetric(rows)
		if err != nil {
			return nil, fmt.Errorf("last metrics: %w", err)
		}
		out[sample.Name+sample.Labels] = sample
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("last metrics: %w", err)
	}
	return out, nil
}

func scanMetric(rows *sql.Rows) (MetricSample, error) {
	var sample MetricSample
	var ts int64
	if err := rows.Scan(&sample.Name, &sample.Labels, &ts, &sample.Value, &sample.Raw); err != nil {
		return MetricSample{}, err
	}
	sample.Time = time.UnixMilli(ts).UTC()
	return sample, nil
}
