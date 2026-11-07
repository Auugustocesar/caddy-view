# Phase 6 — The redesign, and knowing when an upstream is down

Goal: a dashboard that tells you what needs attention instead of showing everything with the same weight, and that is
right about which upstreams are up. Two things drive it. The redesign (flat, typographic, one strong surface, one
orange accent) was drawn and reviewed in an Artifact. And a look at a real Caddy (a homelab with 12 sites) showed that
Caddy itself does not know when an upstream is down.

Everything here is built tests first (`docs/PLAN.md`, section 11). **Slices 6a to 6e are done, and
the design of all seven screens is closed for v1.**

The design lives in a private Artifact (it holds real hostnames), so it is not linked here. What it settled, beyond the
Dashboard decisions below:

- **Logs:** a filter toolbar (search, 2xx to 5xx pills, host, Pause, Clear), a table on one surface, and a side panel
  with the selected request.
- **Sites:** the managed sites as plain rows with an enable switch, the new-site form beside them, and the Caddyfile
  that would be written below.
- **Caddyfile:** three separate areas. State in a strip (Caddy, changed lines, check, loaded revision); the editor with
  an orange bar in the margin for edited lines only; and the revisions list. An error colours the whole line and puts
  the message right under it; Apply is off while the check has an error.
- **Metrics:** the same figures compared with the previous period, a traffic chart, and a host table where slow and
  failing hosts are marked.
- **Config:** tabs, with Status first; **Sign in:** one card with error and rate-limited states.

## What the real Caddy showed

Read-only, on a Caddy v2.11.9 with one wildcard site, `*.example.com`, and one route per subdomain inside it:

- **Sites nested in a wildcard were merged.** `ExtractSites` read the host only on the route at the top of the server,
  so the wildcard showed as one site with 12 upstreams mixed together. Fixed in 6a.
- **Caddy does not know an upstream is down unless it has health checks.** A stopped container showed `fails = 0` in
  `/reverse_proxy/upstreams` and `caddy_reverse_proxy_upstreams_healthy = 1` in `/metrics`. The configuration had no
  health checks.
- **A default native health check would be wrong.** Six of the twelve upstreams answer something that is not a 2xx on
  `/` (404, 400, 301, 302, 307), and Caddy counts only the status code you name as healthy.
- **Traffic is small.** About 10 requests a minute, so "requests per second" is the wrong unit; the dashboard shows the
  total and a per-minute figure.
- Every site had exactly one upstream, so grouping by site changes little there. It matters when a site has two or
  more upstreams, because then "1 of 2 down" and "0 of 1 down" mean different things.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Upstream health | caddy-view checks the upstreams itself; Caddy's native health checks stay optional | The check needs no per-site setting, and any HTTP answer counts, so 404 and 307 are "answering" |
| What "answering" means | The upstream accepts a connection and answers an HTTP request to `/` with any status, within about 2 s; it does not follow redirects and sends no credentials | Same reasoning; the answer, not its content, says the upstream is there |
| When it cannot be known | State `unknown`, never `down`, when caddy-view cannot reach the upstream's network or the check is off | A wrong "down" is worse than no answer |
| Attention rules | Relative, nothing to configure: a host is slow at 3 times the median p95 of all hosts, errors are flagged at 2 times the previous period, an upstream when its check fails | A first version with no settings; the thresholds are constants, with a floor so tiny values do not flag |
| Numbers | Compared with the previous period of the same length, as the range selector says; the marker on a bar is that period | The text never names a period the selector can change |
| Upstream list | Failing first, a few healthy, the rest behind "Show N more", which opens in place with "Show less" | The ones that matter are never hidden |
| Upstream overview | A strip with one segment per upstream, failing first, instead of dots | Scales from 8 to about 50 |
| Explanations | An "i" with a hover or focus tooltip, once on the section title and on the stats that carry a marker | No paragraph of rules on the screen |
| Typography | Bricolage Grotesque for titles and figures, Geist for text, Geist Mono for labels | A first version in Inter looked like a template |

## Slices

| Slice | What | Done when |
|---|---|---|
| 6a (done) | `ExtractSites` follows hosts inside subroutes | A wildcard with one route per subdomain gives one site per subdomain; the old behaviour is kept for hosts without inner host routes |
| 6b (done) | Upstream check in caddy-view | Each upstream of `/api/sites` has a `state` (`answering`, `down`, `unknown`), a latency and the time of the check |
| 6c (done) | Attention and comparison in the API | `/api/summary` and `/api/hosts` carry the previous period; a route returns the attention items with their numbers |
| 6d (done) | The new interface | The Dashboard first, then Logs, Sites, Caddyfile, Metrics, Config and Sign in, from the tokens of the redesign |
| 6e (done) | Optional native health checks | The Dashboard says what a native check needs, from the statuses the check saw, and warns when `/` is not a 2xx |

### 6a in detail

A route can fan out by host: `*.example.com { @app host app.example.com … }` is adapted to a route for the wildcard
whose subroute holds one route per subdomain, and each of those holds another subroute with the proxy. `routeSites`
follows them with these rules, each with a test:

- A route without a host matcher inside a subroute serves the host of the route that holds it, as before.
- A route that fans out by host is not listed itself, unless it proxies on its own or a route inside it has no host and
  proxies. A top-level host is always listed, so a redirect or a file server is not lost.
- A fallback inside the wildcard that proxies nothing (`abort`) is not a site.
- The same subdomain in several inner routes is one site, with its upstreams merged without repeats.
- Checked against the real configuration: 12 sites, each with its upstream, and no wildcard.

### 6b in detail

`internal/upstreamcheck` has a `Checker` (one `GET /`, no redirects, no credentials, a `User-Agent` that names caddy-view) and
a `Monitor` that keeps the latest result per address, refreshed every 10 s with at most 8 checks at once, and forgets
upstreams that are no longer configured. `/api/sites` adds `state`, `latencyMs` and `checkedAt` to each upstream, and
`upstreamCheck` to the response; `--no-upstream-check` turns it off and every upstream is then `unknown`.

- **Down** is an address that was reached and did not answer: refused, reset or hung up, or no answer in 2 s.
- **Unknown** is everything else: a network or host that cannot be reached, a name that does not resolve (a lookup that
  times out included), a unix socket, a placeholder, an address without a single port, or a check that was stopped.
- A timeout is counted as down. In Docker without a route to the upstream the packets are usually dropped, so such an
  upstream shows as down and not unknown; only an unreachable network or host answers with the error that gives it away.
- An `https://` prefix in the address is checked over TLS without verifying the certificate. The transport TLS setting
  that Caddy keeps beside the address is not read yet; a TLS-only upstream still answers a plain request, with an error
  status, which counts.

### 6c in detail

`internal/attention` holds the rules, as constants with a floor so tiny values do not flag: a host is slow at 3 times
the median p95 of all hosts and at least 100 ms; errors are flagged at 2 times the previous period's 5xx rate, with at
least 3 errors and 0.5 % of the requests; hosts with fewer than 20 requests are skipped; an upstream is flagged when its
check says down, `bad` when all upstreams of the site are down and `warn` when only some are (unknown is never flagged).
"Caddy has no health checks" is said once, and only when some upstream is down. `/api/attention?window=` returns the
items worst first, and `/api/summary` carries `previous`: the period of the same length that ends where the bucket
holding the start of this one begins, so no bucket is counted twice.

### 6d in detail

The Dashboard is the one in the design: key figures compared with the previous period, the traffic as a line with the
previous period dashed beside it, "Needs attention" with one "i" tooltip on its title, the status codes as one bar and
the top hosts and the sites and upstreams (failing first, "Show N more" opening in place) side by side as three columns of one row. Many down sites become one item (four or more). The range (1h, 24h, 7d) is a control of the page; `1h` reads by
the minute. The tokens of the design (dark and light, orange accent, Bricolage Grotesque, Geist and Geist Mono) are in
`web/src/app.css`; the fonts are bundled, latin only, and the content security policy gained `font-src 'self'`, so the
UI still loads nothing from another host.

The other screens follow their boards too: **Logs** has the filter toolbar (search, 2xx to 5xx pills, host) over a
table on one surface, and a side panel with the request that was chosen (the log stream now carries the size and the
client); **Sites** has the managed file strip, the sites as rows (name over a line in mono, a tag for proxy or redirect, the TLS, a switch to enable) and the form of a new site as a panel that is always beside them, and that Edit fills with a site; **Caddyfile** has the
state, the editor and the revisions apart, edited lines marked only by a bar in the margin, an error as the colour of
its line with the message right under it, and Apply and Revert beside the title; **Metrics** has its figures compared with the previous period, the traffic as a line against that period, the same
"Needs attention" as the Dashboard (for 1h, 24h and 7d), and a hosts table with p95, 5xx, data sent and a bar of status
classes, where the slow and failing hosts are marked; **Config** has
flat sections with a hairline instead of cards; **Sign in** is one card with the reset hint. The tabs of the Config
board (Status, Backups, Account, About) and the Dashboard-style "Caddy reachable" item in the Caddyfile strip are not
built: the screen has no data for them yet.

### 6e in detail

Native health checks stay optional and caddy-view never writes them. What it does is tell: the attention item "Caddy has
no health checks" adds how many upstreams answer something other than 2xx on `/`, since Caddy counts only the status
code you name as healthy (`health_status`) at the path you name (`health_uri`).

### 6b, the open points

- **Where it runs.** In Docker, caddy-view may not reach the network of the upstreams. Then the answer is `unknown`,
  and the interface says so instead of showing green.
- **HTTPS upstreams.** An upstream whose transport has TLS is checked over TLS without verifying the certificate, since
  the check asks "is it there", not "is the certificate good".
- **Cost.** One connection per upstream every 10 s, with a limit on how many run at once; `--no-upstream-check` turns it
  off. It sends a `GET /` to services the user owns, which is worth saying in the README.

### Not decided

- Grouping the upstream list by site. It pays off only with two or more upstreams per site.
- Whether "Show more" opens in place or scrolls inside the card; in place makes the column long.
- Where "Open upstreams" goes, since the app has no page of its own for them.
