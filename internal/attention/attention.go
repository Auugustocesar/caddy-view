// Package attention decides what on the dashboard needs a look, without settings: every rule compares a value with
// something the installation already has. A host is slow against the other hosts, errors are up against the previous
// period, and an upstream is down when its check says so.
package attention

import (
	"cmp"
	"slices"
	"sort"
)

// Kind says what an Item is about.
type Kind string

// The kinds of Item.
const (
	// UpstreamDown is a site with an upstream that does not answer.
	UpstreamDown Kind = "upstream_down"
	// ErrorsUp is a host whose 5xx rate is at least double that of the previous period.
	ErrorsUp Kind = "errors_up"
	// HostSlow is a host whose p95 is far above the median of the hosts.
	HostSlow Kind = "host_slow"
	// NoHealthChecks says Caddy has no health checks of its own, so it keeps sending visitors to a down upstream.
	NoHealthChecks Kind = "no_health_checks"
)

// Severity is how much an Item matters.
type Severity string

// The severities, worst first.
const (
	Bad  Severity = "bad"
	Warn Severity = "warn"
	Info Severity = "info"
)

// The thresholds. They are constants and not settings on purpose: the first version has nothing to configure.
const (
	// slowFactor is how many times the median p95 a host must reach to be slow.
	slowFactor = 3
	// slowFloorMs keeps a host from being slow only because everything else is very fast.
	slowFloorMs = 100
	// errorFactor is how many times the previous 5xx rate the current one must reach.
	errorFactor = 2
	// errorFloorRate and errorFloorCount keep a few errors on little traffic from being a trend.
	errorFloorRate  = 0.005
	errorFloorCount = 3
	// minRequests is the fewest requests a host needs for its numbers to say anything.
	minRequests = 20
)

// State is what the upstream check found, as upstreamcheck.State spells it.
type State string

// The states of an upstream.
const (
	Answering State = "answering"
	Down      State = "down"
	Unknown   State = "unknown"
)

// HostStats is what a host received over one period.
type HostStats struct {
	Host      string
	Requests  int64
	Errors5xx int64
	// P95Ms is nil when no latency was recorded.
	P95Ms *float64
}

// UpstreamState is the check of one upstream.
type UpstreamState struct {
	Address string
	State   State
}

// SiteState is a site with the check of its upstreams.
type SiteState struct {
	Host      string
	Upstreams []UpstreamState
}

// Input is everything Evaluate looks at.
type Input struct {
	// Hosts is the selected period, and Previous the period of the same length right before it.
	Hosts, Previous []HostStats
	Sites           []SiteState
	// CaddyHasNoHealthChecks is true when the configuration has reverse proxies and none has health checks.
	CaddyHasNoHealthChecks bool
}

// Item is one thing that needs a look, with the numbers behind it so the reader can judge.
type Item struct {
	Kind     Kind     `json:"kind"`
	Severity Severity `json:"severity"`
	Host     string   `json:"host,omitempty"`
	// Down lists the upstreams that do not answer, out of Total, for UpstreamDown.
	Down  []string `json:"down,omitempty"`
	Total int      `json:"total,omitempty"`
	// P95Ms and MedianP95Ms are for HostSlow.
	P95Ms       *float64 `json:"p95Ms,omitempty"`
	MedianP95Ms *float64 `json:"medianP95Ms,omitempty"`
	// Errors5xx, Rate and PreviousRate are for ErrorsUp. PreviousRate is nil when the previous period had no errors.
	Errors5xx    int64    `json:"errors5xx,omitempty"`
	Rate         *float64 `json:"rate,omitempty"`
	PreviousRate *float64 `json:"previousRate,omitempty"`
}

// Evaluate returns the items that need a look, the worst first.
func Evaluate(in Input) []Item {
	items := []Item{}
	items = append(items, downUpstreams(in.Sites)...)
	items = append(items, errorsUp(in.Hosts, in.Previous)...)
	items = append(items, slowHosts(in.Hosts)...)
	if in.CaddyHasNoHealthChecks && slices.ContainsFunc(items, func(it Item) bool { return it.Kind == UpstreamDown }) {
		items = append(items, Item{Kind: NoHealthChecks, Severity: Info})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if c := cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)); c != 0 {
			return c < 0
		}
		if c := cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)); c != 0 {
			return c < 0
		}
		return a.Host < b.Host
	})
	return items
}

func severityRank(s Severity) int {
	switch s {
	case Bad:
		return 0
	case Warn:
		return 1
	}
	return 2
}

func kindRank(k Kind) int {
	switch k {
	case UpstreamDown:
		return 0
	case ErrorsUp:
		return 1
	case HostSlow:
		return 2
	}
	return 3
}

func downUpstreams(sites []SiteState) []Item {
	var out []Item
	for _, s := range sites {
		var down []string
		for _, u := range s.Upstreams {
			if u.State == Down {
				down = append(down, u.Address)
			}
		}
		if len(down) == 0 {
			continue
		}
		sev := Warn // some upstreams still answer: degraded
		if len(down) == len(s.Upstreams) {
			sev = Bad // visitors get an error
		}
		out = append(out, Item{Kind: UpstreamDown, Severity: sev, Host: s.Host, Down: down, Total: len(s.Upstreams)})
	}
	return out
}

func errorsUp(hosts, previous []HostStats) []Item {
	before := make(map[string]HostStats, len(previous))
	for _, p := range previous {
		before[p.Host] = p
	}
	var out []Item
	for _, h := range hosts {
		if h.Requests < minRequests || h.Errors5xx < errorFloorCount {
			continue
		}
		rate := float64(h.Errors5xx) / float64(h.Requests)
		if rate < errorFloorRate {
			continue
		}
		var prevRate *float64
		if p, ok := before[h.Host]; ok && p.Requests > 0 && p.Errors5xx > 0 {
			r := float64(p.Errors5xx) / float64(p.Requests)
			if rate < errorFactor*r {
				continue
			}
			prevRate = &r
		}
		out = append(out, Item{Kind: ErrorsUp, Severity: Bad, Host: h.Host, Errors5xx: h.Errors5xx, Rate: &rate, PreviousRate: prevRate})
	}
	return out
}

func slowHosts(hosts []HostStats) []Item {
	var p95s []float64
	for _, h := range hosts {
		if h.P95Ms != nil {
			p95s = append(p95s, *h.P95Ms)
		}
	}
	if len(p95s) < 2 {
		return nil
	}
	median := median(p95s)
	var out []Item
	for _, h := range hosts {
		if h.P95Ms == nil || h.Requests < minRequests {
			continue
		}
		if *h.P95Ms >= slowFloorMs && *h.P95Ms >= slowFactor*median {
			p95, m := *h.P95Ms, median
			out = append(out, Item{Kind: HostSlow, Severity: Warn, Host: h.Host, P95Ms: &p95, MedianP95Ms: &m})
		}
	}
	return out
}

func median(values []float64) float64 {
	s := slices.Clone(values)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
