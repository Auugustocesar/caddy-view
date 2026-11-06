package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// upstreamStates returns the check state of the only upstream of the only site, and whether the API says the check runs.
func upstreamStates(t *testing.T, base string) (state string, checkRuns bool) {
	t.Helper()
	resp, err := http.Get(base + "/api/sites")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Sites []struct {
			Upstreams []struct {
				State string `json:"state"`
			} `json:"upstreams"`
		} `json:"sites"`
		UpstreamCheck bool `json:"upstreamCheck"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sites) != 1 || len(body.Sites[0].Upstreams) != 1 {
		t.Fatalf("sites = %+v, want one site with one upstream", body.Sites)
	}
	return body.Sites[0].Upstreams[0].State, body.UpstreamCheck
}

func proxyConfigTo(addr string) string {
	return fmt.Sprintf(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["app.example.com"]}],`+
		`"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":%q}]}]}]}}}}}`, addr)
}

func TestServeChecksUpstreams(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // any answer counts
	}))
	t.Cleanup(upstream.Close)
	admin := fakeAdmin(t, proxyConfigTo(strings.TrimPrefix(upstream.URL, "http://")))
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--no-persist")
	t.Cleanup(func() { stop() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		state, runs := upstreamStates(t, base)
		if !runs {
			t.Fatal("upstreamCheck = false, want true by default")
		}
		if state == "answering" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q after 5s, want answering", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServeNoUpstreamCheck(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the upstream was asked although --no-upstream-check is on")
	}))
	t.Cleanup(upstream.Close)
	admin := fakeAdmin(t, proxyConfigTo(strings.TrimPrefix(upstream.URL, "http://")))
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--no-persist", "--no-upstream-check")

	state, runs := upstreamStates(t, base)
	if runs || state != "unknown" {
		t.Errorf("state = %q, upstreamCheck = %v; want unknown and false", state, runs)
	}
	stop() // after the assertions, so that a check that started late would have been caught by the handler above
}
