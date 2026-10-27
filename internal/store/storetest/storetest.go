// Package storetest holds the contract that every store implementation must satisfy, so the in-memory store used by
// tests cannot drift from the SQLite store used in production.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// Store is the part of a store the contract checks.
type Store interface {
	AppendLog(ctx context.Context, batch store.LogBatch) error
	QueryLogs(ctx context.Context, q store.LogQuery) ([]store.LogEntry, error)
	LoadCursor(ctx context.Context, path string) (store.Cursor, bool, error)
	DeleteLogsBefore(ctx context.Context, t time.Time) (int64, error)
	Close() error
}

// base is a round time, so that millisecond rounding never changes it.
var base = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func entry(host, path string, status int, at time.Time) store.LogEntry {
	return store.LogEntry{Time: at, Host: host, Method: "GET", Path: path, Status: status}
}

func batchOf(entries ...store.LogEntry) store.LogBatch { return store.LogBatch{Entries: entries} }

func mustAppend(t *testing.T, s Store, entries ...store.LogEntry) {
	t.Helper()
	if err := s.AppendLog(context.Background(), batchOf(entries...)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
}

func mustQuery(t *testing.T, s Store, q store.LogQuery) []store.LogEntry {
	t.Helper()
	got, err := s.QueryLogs(context.Background(), q)
	if err != nil {
		t.Fatalf("QueryLogs(%+v): %v", q, err)
	}
	return got
}

func paths(entries []store.LogEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Run checks the contract against stores made by newStore. Every subtest gets a fresh store.
func Run(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	fresh := func(t *testing.T) Store {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}

	t.Run("an entry reads back as it was stored", func(t *testing.T) {
		s := fresh(t)
		want := store.LogEntry{
			Time: base.Add(1500 * time.Millisecond), Host: "app.example.com", Method: "POST", Path: "/v1/items",
			Status: 502, Duration: 12345 * time.Microsecond, Bytes: 4096, ClientIP: "10.0.0.7",
		}
		mustAppend(t, s, want)

		got := mustQuery(t, s, store.LogQuery{})
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1", len(got))
		}
		if got[0].ID <= 0 {
			t.Errorf("ID = %d, want the store to assign a positive one", got[0].ID)
		}
		if !got[0].Time.Equal(want.Time) || got[0].Time.Location() != time.UTC {
			t.Errorf("Time = %v, want %v in UTC", got[0].Time, want.Time)
		}
		got[0].ID, got[0].Time, want.Time = 0, time.Time{}, time.Time{}
		if got[0] != want {
			t.Errorf("entry = %+v, want %+v", got[0], want)
		}
	})

	t.Run("an ID given on insert is ignored", func(t *testing.T) {
		s := fresh(t)
		e := entry("a.example.com", "/x", 200, base)
		e.ID = 9999
		mustAppend(t, s, e)
		if got := mustQuery(t, s, store.LogQuery{}); got[0].ID == 9999 {
			t.Errorf("the store used the caller's ID %d", got[0].ID)
		}
	})

	t.Run("entries come back newest first, by insertion order", func(t *testing.T) {
		s := fresh(t)
		// The times are not in order: the contract orders by when entries were stored.
		mustAppend(t, s, entry("h", "/first", 200, base.Add(2*time.Second)))
		mustAppend(t, s, entry("h", "/second", 200, base))
		mustAppend(t, s, entry("h", "/third", 200, base.Add(time.Second)))

		got := paths(mustQuery(t, s, store.LogQuery{}))
		if want := []string{"/third", "/second", "/first"}; !equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("the limit", func(t *testing.T) {
		s := fresh(t)
		many := make([]store.LogEntry, 1005)
		for i := range many {
			many[i] = entry("h", fmt.Sprintf("/p%d", i), 200, base)
		}
		mustAppend(t, s, many...)

		for _, tt := range []struct {
			name  string
			limit int
			want  int
		}{
			{"zero means the default", 0, store.DefaultLimit},
			{"an explicit limit", 5, 5},
			{"a limit above the maximum is capped", 5000, store.MaxLimit},
		} {
			if got := mustQuery(t, s, store.LogQuery{Limit: tt.limit}); len(got) != tt.want {
				t.Errorf("%s: got %d entries, want %d", tt.name, len(got), tt.want)
			}
		}
		_, err := s.QueryLogs(context.Background(), store.LogQuery{Limit: -1})
		if !errors.Is(err, store.ErrInvalidQuery) {
			t.Errorf("negative limit: error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("filters", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s,
			entry("a.example.com", "/index", 200, base),
			entry("a.example.com", "/Login", 302, base.Add(time.Minute)),
			entry("b.example.com", "/login", 404, base.Add(2*time.Minute)),
			entry("b.example.com", "/api/100%", 500, base.Add(3*time.Minute)),
			entry("b.example.com", "/api/100x", 503, base.Add(4*time.Minute)),
			entry("b.example.com", `/a_b\c`, 200, base.Add(5*time.Minute)),
			entry("b.example.com", "/aXb\\c", 200, base.Add(6*time.Minute)),
		)

		tests := []struct {
			name  string
			query store.LogQuery
			want  []string // newest first
		}{
			{"host is matched exactly", store.LogQuery{Host: "a.example.com"}, []string{"/Login", "/index"}},
			{"a host that is only a prefix does not match", store.LogQuery{Host: "a.example"}, []string{}},
			{"minimum status", store.LogQuery{MinStatus: 500}, []string{"/api/100x", "/api/100%"}},
			{"maximum status", store.LogQuery{MaxStatus: 302}, []string{"/aXb\\c", `/a_b\c`, "/Login", "/index"}},
			{"a status range includes both ends", store.LogQuery{MinStatus: 302, MaxStatus: 404}, []string{"/login", "/Login"}},
			{"time range includes From and excludes To", store.LogQuery{From: base.Add(time.Minute), To: base.Add(3 * time.Minute)}, []string{"/login", "/Login"}},
			{"only From", store.LogQuery{From: base.Add(5 * time.Minute)}, []string{"/aXb\\c", `/a_b\c`}},
			{"only To", store.LogQuery{To: base.Add(time.Minute)}, []string{"/index"}},
			{"text ignores case", store.LogQuery{Text: "LOGIN"}, []string{"/login", "/Login"}},
			{"a percent sign in the text is literal", store.LogQuery{Text: "100%"}, []string{"/api/100%"}},
			{"an underscore in the text is literal", store.LogQuery{Text: "a_b"}, []string{`/a_b\c`}},
			{"a backslash in the text is literal", store.LogQuery{Text: `b\c`}, []string{"/aXb\\c", `/a_b\c`}},
			{"filters combine", store.LogQuery{Host: "b.example.com", Text: "login", MaxStatus: 404}, []string{"/login"}},
			{"no filter matches nothing here", store.LogQuery{Text: "nothing like this"}, []string{}},
		}
		for _, tt := range tests {
			got := paths(mustQuery(t, s, tt.query))
			if !equal(got, tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		}
	})

	t.Run("BeforeID pages backwards without gaps or repeats", func(t *testing.T) {
		s := fresh(t)
		for i := 0; i < 10; i++ {
			mustAppend(t, s, entry("h", fmt.Sprintf("/p%d", i), 200, base))
		}

		var seen []string
		var before int64
		for page := 0; page < 10; page++ {
			got := mustQuery(t, s, store.LogQuery{Limit: 4, BeforeID: before})
			if len(got) == 0 {
				break
			}
			seen = append(seen, paths(got)...)
			before = got[len(got)-1].ID
		}
		want := []string{"/p9", "/p8", "/p7", "/p6", "/p5", "/p4", "/p3", "/p2", "/p1", "/p0"}
		if !equal(seen, want) {
			t.Errorf("pages gave %v, want %v", seen, want)
		}
	})

	t.Run("a bad entry rejects the whole batch", func(t *testing.T) {
		s := fresh(t)
		ctx := context.Background()
		before := store.Cursor{Path: "/var/log/access.log", FileID: "1", Offset: 10}
		if err := s.AppendLog(ctx, store.LogBatch{Entries: []store.LogEntry{entry("h", "/kept", 200, base)}, Cursor: before}); err != nil {
			t.Fatal(err)
		}

		for name, bad := range map[string]store.LogEntry{
			"zero time":  {Host: "h", Path: "/bad", Status: 200},
			"empty host": {Time: base, Path: "/bad", Status: 200},
		} {
			batch := store.LogBatch{
				Entries: []store.LogEntry{entry("h", "/good", 200, base), bad},
				Cursor:  store.Cursor{Path: "/var/log/access.log", FileID: "1", Offset: 99},
			}
			err := s.AppendLog(ctx, batch)
			if !errors.Is(err, store.ErrInvalidEntry) {
				t.Errorf("%s: error = %v, want ErrInvalidEntry", name, err)
			}
		}

		if got := paths(mustQuery(t, s, store.LogQuery{})); !equal(got, []string{"/kept"}) {
			t.Errorf("entries after the rejected batches = %v, want only /kept", got)
		}
		got, ok, err := s.LoadCursor(ctx, before.Path)
		if err != nil || !ok || got.Offset != 10 {
			t.Errorf("cursor after the rejected batches = %+v (found %v, err %v), want offset 10 unchanged", got, ok, err)
		}
	})

	t.Run("cursors", func(t *testing.T) {
		s := fresh(t)
		ctx := context.Background()

		if _, ok, err := s.LoadCursor(ctx, "/nope.log"); err != nil || ok {
			t.Fatalf("LoadCursor of an unknown path = found %v, err %v, want not found and no error", ok, err)
		}

		a := store.Cursor{Path: "/a.log", FileID: "7:42", Fingerprint: "abc", Offset: 1234, SkippedLines: 2, Gaps: 1}
		b := store.Cursor{Path: "/b.log", FileID: "7:43", Offset: 5}
		for _, c := range []store.Cursor{a, b} {
			if err := s.AppendLog(ctx, store.LogBatch{Cursor: c}); err != nil {
				t.Fatal(err)
			}
		}
		if got, ok, err := s.LoadCursor(ctx, "/a.log"); err != nil || !ok || got != a {
			t.Errorf("cursor a = %+v (found %v, err %v), want %+v", got, ok, err, a)
		}

		a.Offset = 5000
		if err := s.AppendLog(ctx, store.LogBatch{Cursor: a}); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.LoadCursor(ctx, "/a.log"); got != a {
			t.Errorf("cursor a after an update = %+v, want %+v", got, a)
		}
		if got, _, _ := s.LoadCursor(ctx, "/b.log"); got != b {
			t.Errorf("cursor b = %+v, want it untouched: %+v", got, b)
		}
	})

	t.Run("a batch without a cursor path only stores entries", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, entry("h", "/x", 200, base))
		if got := mustQuery(t, s, store.LogQuery{}); len(got) != 1 {
			t.Errorf("got %d entries, want 1", len(got))
		}
	})

	t.Run("DeleteLogsBefore removes older entries and keeps the boundary", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s,
			entry("h", "/old1", 200, base),
			entry("h", "/old2", 200, base.Add(time.Second)),
			entry("h", "/edge", 200, base.Add(2*time.Second)),
			entry("h", "/new", 200, base.Add(3*time.Second)),
		)

		n, err := s.DeleteLogsBefore(context.Background(), base.Add(2*time.Second))
		if err != nil || n != 2 {
			t.Fatalf("DeleteLogsBefore = %d, %v, want 2 removed", n, err)
		}
		if got := paths(mustQuery(t, s, store.LogQuery{})); !equal(got, []string{"/new", "/edge"}) {
			t.Errorf("entries left = %v, want [/new /edge]", got)
		}
		if n, err := s.DeleteLogsBefore(context.Background(), base); err != nil || n != 0 {
			t.Errorf("deleting nothing = %d, %v, want 0", n, err)
		}
	})

	t.Run("a cancelled context stops every method", func(t *testing.T) {
		s := fresh(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		checks := map[string]error{
			"AppendLog": s.AppendLog(ctx, batchOf(entry("h", "/x", 200, base))),
		}
		_, checks["QueryLogs"] = s.QueryLogs(ctx, store.LogQuery{})
		_, _, checks["LoadCursor"] = s.LoadCursor(ctx, "/a.log")
		_, checks["DeleteLogsBefore"] = s.DeleteLogsBefore(ctx, base)
		for name, err := range checks {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s with a cancelled context: error = %v, want context.Canceled", name, err)
			}
		}
		if got := mustQuery(t, s, store.LogQuery{}); len(got) != 0 {
			t.Errorf("a cancelled AppendLog stored %d entries", len(got))
		}
	})

	t.Run("concurrent writers and readers do not lose entries", func(t *testing.T) {
		s := fresh(t)
		const writers, perWriter = 8, 50
		var wg sync.WaitGroup
		errs := make(chan error, writers+4)
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perWriter; i++ {
					e := entry("h", fmt.Sprintf("/w%d-%d", w, i), 200, base)
					if err := s.AppendLog(context.Background(), batchOf(e)); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					if _, err := s.QueryLogs(context.Background(), store.LogQuery{Limit: 20}); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("concurrent use: %v", err)
		}
		got := mustQuery(t, s, store.LogQuery{Limit: store.MaxLimit})
		if len(got) != writers*perWriter {
			t.Errorf("stored %d entries, want %d", len(got), writers*perWriter)
		}
	})

	t.Run("Close can be called twice and later calls fail", func(t *testing.T) {
		s := newStore(t)
		if err := s.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
		if err := s.AppendLog(context.Background(), batchOf(entry("h", "/x", 200, base))); err == nil {
			t.Error("AppendLog after Close succeeded, want an error")
		}
		if _, err := s.QueryLogs(context.Background(), store.LogQuery{}); err == nil {
			t.Error("QueryLogs after Close succeeded, want an error")
		}
	})
}
