// Package instancelock lets one process at a time own a resource, such as the Caddyfile that caddy-view edits.
//
// The lock is an exclusive SQLite transaction on a small file, held on one connection for as long as the lock is held.
// SQLite takes the operating system's file locks for it, and the system drops them when the process ends, however it
// ends, so there is no stale lock to clear, no PID file and no timeout. It reuses the SQLite driver the history store
// already needs and works the same on Linux, macOS and Windows. It holds between processes on one machine that see the
// file through the same local file system; it is not meant for network file systems.
package instancelock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// ErrBusy means another holder, in this process or in another, has the lock.
var ErrBusy = errors.New("the lock is held by another process")

// Lock is a held lock. The zero value holds nothing.
type Lock struct {
	mu   sync.Mutex
	db   *sql.DB
	conn *sql.Conn
}

// Acquire takes the lock kept in the file at path, creating the file if needed, without waiting. It fails with
// ErrBusy when somebody else holds it, and with another error when the file cannot be used: its directory is missing
// or not writable, or the path is a symbolic link. The lock is released by Release, or by the system when the process
// ends.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Never follow a symbolic link that somebody planted at the lock path.
	if info, err := os.Lstat(path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("lock file %s is a symbolic link", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}

	// No waiting: busy_timeout 0 makes a held lock an immediate SQLITE_BUSY. A rollback journal, not WAL, so that
	// the lock is a plain file lock.
	db, err := sql.Open("sqlite", store.SQLiteURI(path, []string{"busy_timeout(0)", "journal_mode(DELETE)"}, ""))
	if err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err == nil {
		_, err = conn.ExecContext(ctx, "BEGIN EXCLUSIVE")
		if err != nil {
			conn.Close() // the error that matters is the one from BEGIN
		}
	}
	if err != nil {
		db.Close() // same
		if isBusy(err) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock file: %w", err)
	}
	return &Lock{db: db, conn: conn}, nil
}

// isBusy reports whether SQLite refused because somebody else holds the file.
func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_BUSY
}

// Release gives the lock up. It is safe to call more than once.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return nil
	}
	_, rollbackErr := l.conn.ExecContext(context.Background(), "ROLLBACK")
	connErr := l.conn.Close()
	dbErr := l.db.Close()
	l.db, l.conn = nil, nil
	return errors.Join(rollbackErr, connErr, dbErr)
}
