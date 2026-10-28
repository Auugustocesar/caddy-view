package storetest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// SiteStore is the part of a store the managed sites contract checks.
type SiteStore interface {
	Sites(ctx context.Context) ([]store.Site, error)
	ReplaceSites(ctx context.Context, sites []store.Site) error
	AddRevision(ctx context.Context, r store.Revision) (store.Revision, error)
	Revision(ctx context.Context, number int64) (store.Revision, error)
	Revisions(ctx context.Context, q store.RevisionQuery) ([]store.Revision, error)
	Close() error
}

func proxySite(id, host string, upstreams ...string) store.Site {
	return store.Site{
		ID: id, Hostname: host, Kind: store.SiteProxy, Upstreams: upstreams, TLS: store.TLSAutomatic, Enabled: true,
		CreatedAt: base, UpdatedAt: base.Add(time.Minute),
	}
}

func redirectSite(id, host, target string) store.Site {
	return store.Site{
		ID: id, Hostname: host, Kind: store.SiteRedirect, RedirectTarget: target, RedirectCode: 301, TLS: store.TLSOff, Enabled: false,
		CreatedAt: base, UpdatedAt: base,
	}
}

func hostnames(sites []store.Site) []string {
	out := make([]string, len(sites))
	for i, s := range sites {
		out[i] = s.Hostname
	}
	return out
}

// RunSites checks the managed sites part of the contract against stores made by newStore. Every subtest gets a fresh
// store.
func RunSites(t *testing.T, newStore func(t *testing.T) SiteStore) {
	t.Helper()
	fresh := func(t *testing.T) SiteStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	ctx := context.Background()

	t.Run("an empty store has no sites", func(t *testing.T) {
		got, err := fresh(t).Sites(ctx)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("Sites = %v, %v, want an empty list that is not nil", got, err)
		}
	})

	t.Run("sites read back as stored, ordered by hostname", func(t *testing.T) {
		s := fresh(t)
		b := proxySite("b", "b.example.com", "10.0.0.2:8080", "https://10.0.0.3:8443")
		b.SkipTLSVerify, b.TLS, b.AccessLog = true, store.TLSInternal, true
		a := redirectSite("a", "a.example.com", "https://new.example.com{uri}")
		if err := s.ReplaceSites(ctx, []store.Site{b, a}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Sites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(hostnames(got), []string{"a.example.com", "b.example.com"}) {
			t.Fatalf("hostnames = %v, want them ordered", hostnames(got))
		}
		if !sameSite(got[0], a) || !sameSite(got[1], b) {
			t.Errorf("Sites = %+v, want %+v and %+v", got, a, b)
		}
	})

	t.Run("replacing keeps only the new set", func(t *testing.T) {
		s := fresh(t)
		if err := s.ReplaceSites(ctx, []store.Site{proxySite("a", "a.example.com", "10.0.0.1")}); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceSites(ctx, []store.Site{proxySite("b", "b.example.com", "10.0.0.2")}); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Sites(ctx); !reflect.DeepEqual(hostnames(got), []string{"b.example.com"}) {
			t.Errorf("hostnames = %v, want only b.example.com", hostnames(got))
		}
		if err := s.ReplaceSites(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Sites(ctx); len(got) != 0 {
			t.Errorf("Sites after replacing with nothing = %v, want none", hostnames(got))
		}
	})

	t.Run("a malformed set is refused and changes nothing", func(t *testing.T) {
		s := fresh(t)
		keep := proxySite("keep", "keep.example.com", "10.0.0.1")
		if err := s.ReplaceSites(ctx, []store.Site{keep}); err != nil {
			t.Fatal(err)
		}
		bad := map[string][]store.Site{
			"a repeated id":                 {proxySite("x", "a.example.com", "10.0.0.1"), proxySite("x", "b.example.com", "10.0.0.2")},
			"a repeated hostname":           {proxySite("a", "same.example.com", "10.0.0.1"), proxySite("b", "same.example.com", "10.0.0.2")},
			"a repeated hostname in a case": {proxySite("a", "same.example.com", "10.0.0.1"), proxySite("b", "SAME.example.com", "10.0.0.2")},
			"no id":                         {proxySite("", "a.example.com", "10.0.0.1")},
			"no hostname":                   {proxySite("a", "", "10.0.0.1")},
			"an unknown kind":               {func() store.Site { s := proxySite("a", "a.example.com", "10.0.0.1"); s.Kind = "magic"; return s }()},
			"an unknown tls mode":           {func() store.Site { s := proxySite("a", "a.example.com", "10.0.0.1"); s.TLS = "maybe"; return s }()},
			"no kind":                       {func() store.Site { s := proxySite("a", "a.example.com", "10.0.0.1"); s.Kind = ""; return s }()},
		}
		for name, set := range bad {
			if err := s.ReplaceSites(ctx, set); !errors.Is(err, store.ErrInvalidSite) {
				t.Errorf("%s: ReplaceSites error = %v, want ErrInvalidSite", name, err)
			}
		}
		got, err := s.Sites(ctx)
		if err != nil || len(got) != 1 || !sameSite(got[0], keep) {
			t.Errorf("Sites after the refusals = %+v, %v, want the original single site untouched", got, err)
		}
	})

	t.Run("what Sites returns is a copy", func(t *testing.T) {
		s := fresh(t)
		if err := s.ReplaceSites(ctx, []store.Site{proxySite("a", "a.example.com", "10.0.0.1")}); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Sites(ctx)
		got[0].Upstreams[0] = "changed"
		got[0].Hostname = "changed.example.com"
		if again, _ := s.Sites(ctx); again[0].Upstreams[0] != "10.0.0.1" || again[0].Hostname != "a.example.com" {
			t.Errorf("the stored site became %+v", again[0])
		}
	})

	t.Run("what ReplaceSites is given is not kept by reference", func(t *testing.T) {
		s := fresh(t)
		site := proxySite("a", "a.example.com", "10.0.0.1")
		if err := s.ReplaceSites(ctx, []store.Site{site}); err != nil {
			t.Fatal(err)
		}
		site.Upstreams[0] = "changed"
		if got, _ := s.Sites(ctx); got[0].Upstreams[0] != "10.0.0.1" {
			t.Errorf("the stored upstream became %q", got[0].Upstreams[0])
		}
	})

	t.Run("a revision keeps its managed state", func(t *testing.T) {
		s := fresh(t)
		snapshot := []store.Site{proxySite("a", "a.example.com", "10.0.0.1"), redirectSite("b", "b.example.com", "https://x.example.com")}
		managed := &store.RevisionSites{Content: "# managed\n", SHA256: strings.Repeat("cd", 32), Snapshot: snapshot}
		r := revision("main", base)
		r.Sites = managed
		added, err := s.AddRevision(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := s.AddRevision(ctx, revision("main without sites", base))
		if err != nil {
			t.Fatal(err)
		}
		if plain.Sites != nil {
			t.Errorf("a revision without managed state came back with %+v", plain.Sites)
		}
		for name, got := range map[string]store.Revision{
			"AddRevision": added,
			"Revision":    mustRevision(t, s, added.Number),
			"Revisions":   mustRevisions(t, s)[1], // newest first: the plain one, then this
		} {
			if !sameManaged(got.Sites, managed) {
				t.Errorf("%s: Sites = %+v, want %+v", name, got.Sites, managed)
			}
		}
		if got := mustRevision(t, s, plain.Number); got.Sites != nil {
			t.Errorf("Revision of a plain revision has Sites %+v, want nil", got.Sites)
		}
	})

	t.Run("a revision shares nothing with the caller", func(t *testing.T) {
		s := fresh(t)
		given := &store.RevisionSites{
			Content: "# managed\n", SHA256: strings.Repeat("cd", 32), Snapshot: []store.Site{proxySite("a", "a.example.com", "10.0.0.1")},
		}
		r := revision("main", base)
		r.Sites = given
		added, err := s.AddRevision(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		// Change what was given, what was returned, and what was read: the stored revision stays as it was.
		given.Content = "mutated"
		given.Snapshot[0].Hostname = "mutated.example.com"
		given.Snapshot[0].Upstreams[0] = "mutated"
		if added.Sites != nil {
			added.Sites.Snapshot[0].Hostname = "mutated.example.com"
		}
		read := mustRevision(t, s, added.Number)
		if read.Sites == nil || len(read.Sites.Snapshot) == 0 {
			t.Fatalf("Revision returned Sites %+v, want the managed state that was stored", read.Sites)
		}
		read.Sites.Snapshot[0].Hostname = "mutated.example.com"
		read.Sites.Content = "mutated"

		again := mustRevision(t, s, added.Number)
		want := &store.RevisionSites{
			Content: "# managed\n", SHA256: strings.Repeat("cd", 32), Snapshot: []store.Site{proxySite("a", "a.example.com", "10.0.0.1")},
		}
		if !sameManaged(again.Sites, want) {
			t.Errorf("the stored managed state became %+v, want %+v", again.Sites, want)
		}
	})

	t.Run("a revision with a malformed managed state is refused", func(t *testing.T) {
		s := fresh(t)
		r := revision("main", base)
		r.Sites = &store.RevisionSites{Content: "x", SHA256: "not a digest"}
		if _, err := s.AddRevision(ctx, r); !errors.Is(err, store.ErrInvalidRevision) {
			t.Errorf("AddRevision error = %v, want ErrInvalidRevision", err)
		}
	})

	t.Run("a closed store refuses the sites too", func(t *testing.T) {
		s := newStore(t)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Sites(ctx); !errors.Is(err, store.ErrClosed) {
			t.Errorf("Sites error = %v, want ErrClosed", err)
		}
		if err := s.ReplaceSites(ctx, nil); !errors.Is(err, store.ErrClosed) {
			t.Errorf("ReplaceSites error = %v, want ErrClosed", err)
		}
	})
}

func mustRevision(t *testing.T, s SiteStore, n int64) store.Revision {
	t.Helper()
	r, err := s.Revision(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustRevisions(t *testing.T, s SiteStore) []store.Revision {
	t.Helper()
	r, err := s.Revisions(context.Background(), store.RevisionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func sameSite(a, b store.Site) bool {
	return a.ID == b.ID && a.Hostname == b.Hostname && a.Kind == b.Kind && reflect.DeepEqual(a.Upstreams, b.Upstreams) &&
		a.SkipTLSVerify == b.SkipTLSVerify && a.TLS == b.TLS && a.RedirectTarget == b.RedirectTarget &&
		a.RedirectCode == b.RedirectCode && a.AccessLog == b.AccessLog && a.Enabled == b.Enabled &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

func sameManaged(a, b *store.RevisionSites) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Content != b.Content || a.SHA256 != b.SHA256 || len(a.Snapshot) != len(b.Snapshot) {
		return false
	}
	for i := range a.Snapshot {
		if !sameSite(a.Snapshot[i], b.Snapshot[i]) {
			return false
		}
	}
	return true
}
