package configmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Auugustocesar/caddy-view/internal/caddyfile"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// ErrAlreadyImported means the Caddyfile already imports the managed file, so there is nothing to add.
var ErrAlreadyImported = errors.New("the Caddyfile already imports the managed file")

// ManagedState is what the interface needs to know about the managed file before it offers to manage sites.
type ManagedState struct {
	// File is the absolute path of the managed file.
	File string
	// Imported is whether the Caddyfile imports it: without that, sites written to it are never served.
	Imported bool
	// AccessLog is whether an access log file is known, so that a site may ask for its requests to be logged.
	AccessLog bool
}

// ManagedState reports where the managed file is and whether the Caddyfile imports it.
func (m *Manager) ManagedState(ctx context.Context) (ManagedState, error) {
	if m.opts.ManagedPath == "" {
		return ManagedState{}, ErrNoManagedFile
	}
	cur, err := m.Current(ctx)
	if err != nil {
		return ManagedState{}, err
	}
	return ManagedState{
		File:      m.opts.ManagedPath,
		Imported:  m.importsManaged(cur.Content),
		AccessLog: m.siteOptions(ctx).AccessLog != "",
	}, nil
}

// AddImport adds the import of the managed file to the Caddyfile, as a revision of its own. It goes through Apply, so
// it has every guard of an edit: the base digest, Caddy's check, the admin and relative-import guards, the verification
// after the load, and the restore on failure.
//
// Caddy refuses to import a file that is not there, so a missing managed file is created empty, which is a state
// ApplySites may overwrite. When the apply fails, a file created here is removed again.
func (m *Manager) AddImport(ctx context.Context) (store.Revision, error) {
	if m.opts.ManagedPath == "" {
		return store.Revision{}, ErrNoManagedFile
	}
	cur, err := m.Current(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	if m.importsManaged(cur.Content) {
		return store.Revision{}, fmt.Errorf("%w: %s", ErrAlreadyImported, m.opts.ManagedPath)
	}
	text, err := caddyfile.AddImport(cur.Content, filepath.ToSlash(m.opts.ManagedPath))
	if err != nil {
		return store.Revision{}, fmt.Errorf("write the import of %s: %w", m.opts.ManagedPath, err)
	}

	prev, err := m.readManaged()
	if err != nil {
		return store.Revision{}, err
	}
	if !prev.existed {
		if err := m.writeManaged(""); err != nil {
			return store.Revision{}, err
		}
	}

	rev, err := m.Apply(ctx, ApplyRequest{Content: text, BaseSHA256: cur.SHA256, Trigger: store.TriggerAddImport})
	// After a failure that left things as they were, a file made for the import has no reason to stay. After one that
	// did not (recovery is needed), nothing is touched: the Caddyfile may already name the file.
	if err != nil && !prev.existed && !errors.Is(err, ErrRecoveryRequired) {
		if rmErr := os.Remove(m.opts.ManagedPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove the managed file made for the import: %w", rmErr))
		}
	}
	return rev, err
}
