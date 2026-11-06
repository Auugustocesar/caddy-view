package attention

import (
	"slices"
	"testing"
)

func ptr(f float64) *float64 { return &f }

// quiet is a set of hosts with nothing wrong: similar latency, no errors.
func quiet() Input {
	return Input{Hosts: []HostStats{
		{Host: "a.example.com", Requests: 1000, P95Ms: ptr(40)},
		{Host: "b.example.com", Requests: 800, P95Ms: ptr(50)},
		{Host: "c.example.com", Requests: 600, P95Ms: ptr(60)},
	}}
}

func kinds(items []Item) []Kind {
	out := make([]Kind, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

func TestEvaluateNothingWrongIsNoItems(t *testing.T) {
	if got := Evaluate(quiet()); len(got) != 0 {
		t.Errorf("Evaluate = %+v, want no items", got)
	}
}

func TestEvaluateSlowHost(t *testing.T) {
	tests := []struct {
		name     string
		p95      float64
		requests int64
		slow     bool
	}{
		{"3 times the median of the hosts", 150, 500, true}, // median of 40, 50, 150 is 50
		{"just under 3 times the median", 149, 500, false},
		{"too few requests to say", 900, 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{Hosts: []HostStats{
				{Host: "a.example.com", Requests: 1000, P95Ms: ptr(40)},
				{Host: "b.example.com", Requests: 800, P95Ms: ptr(50)},
				{Host: "slow.example.com", Requests: tt.requests, P95Ms: ptr(tt.p95)},
			}}
			got := Evaluate(in)
			if tt.slow {
				if len(got) != 1 || got[0].Kind != HostSlow || got[0].Host != "slow.example.com" {
					t.Fatalf("Evaluate = %+v, want one host_slow item for slow.example.com", got)
				}
				if got[0].Severity != Warn {
					t.Errorf("Severity = %q, want %q", got[0].Severity, Warn)
				}
				if got[0].P95Ms == nil || *got[0].P95Ms != tt.p95 || got[0].MedianP95Ms == nil || *got[0].MedianP95Ms != 50 {
					t.Errorf("numbers = p95 %v, median %v; want %v and 50", got[0].P95Ms, got[0].MedianP95Ms, tt.p95)
				}
				return
			}
			if len(got) != 0 {
				t.Errorf("Evaluate = %+v, want no items", got)
			}
		})
	}
}

func TestEvaluateSlowNeedsAFloorSoTinyLatenciesDoNotFlag(t *testing.T) {
	// Every host is fast; one is 4 times the median, but 12 ms is nothing.
	in := Input{Hosts: []HostStats{
		{Host: "a.example.com", Requests: 1000, P95Ms: ptr(3)},
		{Host: "b.example.com", Requests: 1000, P95Ms: ptr(3)},
		{Host: "c.example.com", Requests: 1000, P95Ms: ptr(12)},
	}}
	if got := Evaluate(in); len(got) != 0 {
		t.Errorf("Evaluate = %+v, want no items for latencies under the floor", got)
	}
}

func TestEvaluateOneHostCannotBeSlowerThanItself(t *testing.T) {
	in := Input{Hosts: []HostStats{{Host: "only.example.com", Requests: 1000, P95Ms: ptr(900)}}}
	if got := Evaluate(in); len(got) != 0 {
		t.Errorf("Evaluate = %+v, want no items: with one host there is nothing to compare with", got)
	}
}

func TestEvaluateHostWithoutLatencyIsSkipped(t *testing.T) {
	in := quiet()
	in.Hosts = append(in.Hosts, HostStats{Host: "silent.example.com", Requests: 100})
	if got := Evaluate(in); len(got) != 0 {
		t.Errorf("Evaluate = %+v, want no items", got)
	}
}

func TestEvaluateErrorsUp(t *testing.T) {
	tests := []struct {
		name     string
		now      HostStats
		previous *HostStats
		flagged  bool
	}{
		{"double the previous rate", HostStats{Requests: 1000, Errors5xx: 9}, &HostStats{Requests: 1000, Errors5xx: 3}, true},
		{"exactly double", HostStats{Requests: 1000, Errors5xx: 8}, &HostStats{Requests: 1000, Errors5xx: 4}, true},
		{"just under double", HostStats{Requests: 1000, Errors5xx: 7}, &HostStats{Requests: 1000, Errors5xx: 4}, false},
		{"errors where there were none", HostStats{Requests: 1000, Errors5xx: 12}, &HostStats{Requests: 1000}, true},
		{"errors with no previous period at all", HostStats{Requests: 1000, Errors5xx: 12}, nil, true},
		{"a rate under the floor, doubled", HostStats{Requests: 100000, Errors5xx: 20}, &HostStats{Requests: 100000, Errors5xx: 5}, false},
		{"two errors is not a trend", HostStats{Requests: 100, Errors5xx: 2}, &HostStats{Requests: 100}, false},
		{"the same rate over more traffic", HostStats{Requests: 2000, Errors5xx: 20}, &HostStats{Requests: 1000, Errors5xx: 10}, false},
		{"fewer errors than before", HostStats{Requests: 1000, Errors5xx: 10}, &HostStats{Requests: 1000, Errors5xx: 50}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.now.Host = "api.example.com"
			in := quiet()
			in.Hosts = append(in.Hosts, tt.now)
			if tt.previous != nil {
				p := *tt.previous
				p.Host = "api.example.com"
				in.Previous = []HostStats{p}
			}
			got := Evaluate(in)
			if !tt.flagged {
				if len(got) != 0 {
					t.Errorf("Evaluate = %+v, want no items", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != ErrorsUp || got[0].Host != "api.example.com" {
				t.Fatalf("Evaluate = %+v, want one errors_up item for api.example.com", got)
			}
			if got[0].Severity != Bad {
				t.Errorf("Severity = %q, want %q", got[0].Severity, Bad)
			}
			if got[0].Errors5xx != tt.now.Errors5xx || got[0].Rate == nil {
				t.Errorf("numbers = %+v, want the error count and the rate", got[0])
			}
			if tt.previous != nil && tt.previous.Errors5xx > 0 && got[0].PreviousRate == nil {
				t.Errorf("PreviousRate is missing although the previous period had errors")
			}
		})
	}
}

func TestEvaluateUpstreamsDown(t *testing.T) {
	site := func(host string, states ...State) SiteState {
		s := SiteState{Host: host}
		for i, st := range states {
			s.Upstreams = append(s.Upstreams, UpstreamState{Address: host + ":" + string(rune('1'+i)), State: st})
		}
		return s
	}
	t.Run("a site whose only upstream is down", func(t *testing.T) {
		in := quiet()
		in.Sites = []SiteState{site("admin.example.com", Down), site("ok.example.com", Answering)}
		got := Evaluate(in)
		if len(got) != 1 || got[0].Kind != UpstreamDown || got[0].Host != "admin.example.com" {
			t.Fatalf("Evaluate = %+v, want one upstream_down item for admin.example.com", got)
		}
		if got[0].Severity != Bad || got[0].Total != 1 || len(got[0].Down) != 1 {
			t.Errorf("item = %+v, want bad, 1 of 1 down", got[0])
		}
	})
	t.Run("one of two down is degraded, a lower severity", func(t *testing.T) {
		in := quiet()
		in.Sites = []SiteState{site("reports.example.com", Answering, Down)}
		got := Evaluate(in)
		if len(got) != 1 || got[0].Severity != Warn || got[0].Total != 2 || len(got[0].Down) != 1 {
			t.Fatalf("Evaluate = %+v, want one warn item with 1 of 2 down", got)
		}
	})
	t.Run("unknown is never an item", func(t *testing.T) {
		in := quiet()
		in.Sites = []SiteState{site("far.example.com", Unknown, Unknown)}
		if got := Evaluate(in); len(got) != 0 {
			t.Errorf("Evaluate = %+v, want no items: a wrong alarm is worse than none", got)
		}
	})
	t.Run("a site with no upstreams is not an item", func(t *testing.T) {
		in := quiet()
		in.Sites = []SiteState{{Host: "files.example.com"}}
		if got := Evaluate(in); len(got) != 0 {
			t.Errorf("Evaluate = %+v, want no items", got)
		}
	})
	t.Run("Caddy having no health checks is said once, with a down upstream", func(t *testing.T) {
		in := quiet()
		in.Sites = []SiteState{site("a.example.com", Down), site("b.example.com", Down)}
		in.CaddyHasNoHealthChecks = true
		got := Evaluate(in)
		count := 0
		for _, it := range got {
			if it.Kind == NoHealthChecks {
				count++
				if it.Severity != Info {
					t.Errorf("Severity = %q, want %q", it.Severity, Info)
				}
			}
		}
		if count != 1 {
			t.Errorf("no_health_checks items = %d, want 1 in %v", count, kinds(got))
		}
	})
	t.Run("Caddy having no health checks is not worth saying when nothing is down", func(t *testing.T) {
		in := quiet()
		in.Sites = []SiteState{site("a.example.com", Answering)}
		in.CaddyHasNoHealthChecks = true
		if got := Evaluate(in); len(got) != 0 {
			t.Errorf("Evaluate = %+v, want no items", got)
		}
	})
}

func TestEvaluateOrdersWorstFirst(t *testing.T) {
	in := Input{
		Hosts: []HostStats{
			{Host: "a.example.com", Requests: 1000, P95Ms: ptr(40)},
			{Host: "b.example.com", Requests: 800, P95Ms: ptr(50)},
			{Host: "slow.example.com", Requests: 500, P95Ms: ptr(400)},
			{Host: "err.example.com", Requests: 1000, Errors5xx: 30, P95Ms: ptr(45)},
		},
		Sites:                  []SiteState{{Host: "down.example.com", Upstreams: []UpstreamState{{Address: "x:1", State: Down}}}},
		CaddyHasNoHealthChecks: true,
	}
	got := kinds(Evaluate(in))
	want := []Kind{UpstreamDown, ErrorsUp, HostSlow, NoHealthChecks}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
