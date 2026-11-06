<script>
  import BarChart from '../components/BarChart.svelte'
  import Segmented from '../components/Segmented.svelte'
  import { compare, describeItems } from '../lib/attention.js'
  import { formatCount, formatMs, formatPercent } from '../lib/format.js'
  import { flattenUpstreams, sortFailingFirst, summarizeUpstreams, visibleUpstreams } from '../lib/upstreams.js'

  /**
   * @type {{
   *   status?: {caddy: {reachable: boolean, endpoint: string, error?: string}} | null,
   *   sites?: {sites: Array<{server: string, host: string, upstreams: Array<object>}>, upstreamCheck?: boolean} | null,
   *   summary?: object | null,
   *   traffic?: {resolution?: string, buckets: Array<{start: string, requests: number}>|null} | null,
   *   attention?: Array<object> | null,
   *   range?: '1h'|'24h'|'7d',
   *   onrange?: (range: string) => void,
   *   historyOff?: boolean,
   *   loadError?: string,
   * }}
   */
  let {
    status = null,
    sites = null,
    summary = null,
    traffic = null,
    attention = null,
    range = '24h',
    onrange = () => {},
    historyOff = false,
    loadError = '',
  } = $props()

  const RANGES = [
    { value: '1h', label: '1h' },
    { value: '24h', label: '24h' },
    { value: '7d', label: '7d' },
  ]
  const RANGE_NAMES = { '1h': 'hour', '24h': '24 hours', '7d': '7 days' }
  const SHOWN = 5 // upstreams listed before the rest goes behind "Show N more"

  let expanded = $state(false)

  const rows = $derived(sortFailingFirst(flattenUpstreams(sites)))
  const counts = $derived(summarizeUpstreams(rows))
  const visible = $derived(visibleUpstreams(rows, SHOWN, expanded))
  const items = $derived(attention ? describeItems(attention) : null)

  // What Caddy would say about an upstream with a health check of its own: by default only a 2xx is healthy.
  const notOk = $derived(rows.filter((r) => r.state === 'answering' && r.httpStatus != null && (r.httpStatus < 200 || r.httpStatus > 299)))

  const prev = $derived(summary?.previous)
  const requestsCompare = $derived(prev ? compare(summary.requests, prev.requests) : null)
  const errorsCompare = $derived(prev ? compare(summary?.errorRate, prev.errorRate, { times: true, worseWhenHigher: true }) : null)

  // The upstream figure: caddy-view's own check when it runs, otherwise what Caddy says.
  const upstreamFigure = $derived.by(() => {
    if (sites?.upstreamCheck && rows.length > 0) {
      const parts = []
      if (counts.down > 0) parts.push(`${counts.down} down`)
      if (counts.unknown > 0) parts.push(`${counts.unknown} unknown`)
      const label = [`${counts.answering} answering`, ...parts].join(', ')
      return { value: `${counts.answering} / ${counts.total}`, detail: parts.length ? parts.join(', ') : 'all answering', bad: counts.down > 0, strip: rows, label }
    }
    if (summary?.upstreams) {
      return { value: `${summary.upstreams.healthy} / ${summary.upstreams.total}`, detail: 'healthy, as Caddy says', bad: false, strip: null, label: '' }
    }
    return null
  })

  const trafficTitle = $derived(traffic?.resolution === 'minute' ? 'Requests per minute' : 'Requests per hour')
  const plural = (n, one, many) => (n === 1 ? one : many)
</script>

{#if loadError}
  <p class="error" role="alert">{loadError}</p>
{/if}

{#if status}
  {@const caddy = status.caddy}
  <section class="hero" aria-label="Summary">
    <div>
      <div class="eyebrow">Compared with the previous {RANGE_NAMES[range]}</div>
      <h1>Overview</h1>
    </div>
    <div class="controls">
      <span class="chip" role="status" data-state={caddy.reachable ? 'up' : 'down'}>
        <i class="dot" aria-hidden="true"></i>
        {#if caddy.reachable}Caddy is reachable{:else}Caddy is not reachable{#if caddy.error}<span class="why">: {caddy.error}</span>{/if}{/if}
      </span>
      {#if !historyOff}
        <Segmented label="Range" options={RANGES} value={range} onchange={onrange} />
      {/if}
    </div>
  </section>

  {#if historyOff}
    <p class="note">Traffic history is off, so there are no totals or charts. Start caddy-view with persistence to see them.</p>
  {:else if summary}
    <section class="figures" aria-label="Key figures">
      <div class="figure" role="group" aria-labelledby="f-requests">
        <span class="eyebrow" id="f-requests">Requests</span>
        <strong class="v">{formatCount(summary.requests)}</strong>
        {#if requestsCompare}<span class="n">{requestsCompare.text}</span>{/if}
      </div>
      <div class="figure" role="group" aria-labelledby="f-errors" data-tone={errorsCompare?.tone}>
        <span class="eyebrow" id="f-errors">5xx rate</span>
        <strong class="v">{formatPercent(summary.errorRate)}</strong>
        <span class="n">{formatCount(summary.errors5xx)} of {formatCount(summary.requests)} requests</span>
        {#if errorsCompare}<span class="n compare">{errorsCompare.text}</span>{/if}
      </div>
      <div class="figure" role="group" aria-labelledby="f-latency">
        <span class="eyebrow" id="f-latency">p95 latency</span>
        <strong class="v">{formatMs(summary.p95Ms)}</strong>
        <span class="n">average {formatMs(summary.avgMs)}</span>
      </div>
      {#if upstreamFigure}
        <div class="figure" role="group" aria-labelledby="f-upstreams" data-tone={upstreamFigure.bad ? 'bad' : undefined}>
          <span class="eyebrow" id="f-upstreams">Upstreams</span>
          <strong class="v">{upstreamFigure.value}</strong>
          {#if upstreamFigure.strip}
            <div class="strip" role="img" aria-label={upstreamFigure.label}>
              {#each upstreamFigure.strip as segment}<i data-state={segment.state}></i>{/each}
            </div>
          {/if}
          <span class="n">{upstreamFigure.detail}</span>
        </div>
      {/if}
    </section>
  {/if}

  <div class="main">
    {#if !historyOff && traffic}
      <section class="chartcard" aria-label="Traffic">
        <BarChart title={trafficTitle} buckets={traffic.buckets ?? []} labels={range === '7d' ? 'date' : 'time'} />
      </section>
    {/if}

    {#if items}
      <section class="attention" aria-label="Needs attention">
        <h2>
          Needs attention
          <button class="info" type="button" aria-label="How this is decided">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><circle cx="8" cy="8" r="6.5" stroke="currentColor" /><path d="M8 7.2v3.6" stroke="currentColor" stroke-linecap="round" /><circle cx="8" cy="5" r=".9" fill="currentColor" /></svg>
            <span class="tipbox" role="tooltip">A host is flagged slow at 3 times the median p95 of your hosts, and for errors at 2 times the previous period. An upstream is flagged when its check fails.</span>
          </button>
        </h2>
        {#each items as item (item.key)}
          <div class="item" data-severity={item.severity}>
            <span class="mark" aria-hidden="true"></span>
            <div>
              <div class="t">{item.title}</div>
              <p>{item.text}</p>
              {#if item.severity === 'info' && notOk.length > 0}
                <p>
                  {notOk.length} of your {rows.length}
                  {plural(rows.length, 'upstream', 'upstreams')}
                  {plural(notOk.length, 'does', 'do')} not answer 2xx on /, so a health check of Caddy with its defaults would call
                  {plural(notOk.length, 'it', 'them')} unhealthy. Give the check a <code>health_uri</code> that answers 2xx, or the status to count as healthy in <code>health_status</code>.
                </p>
              {/if}
            </div>
          </div>
        {:else}
          <p class="quiet">Nothing needs attention.</p>
        {/each}
      </section>
    {/if}
  </div>

  {#if caddy.reachable && sites}
    <section class="upstreams" aria-label="Sites and upstreams">
      <div class="head">
        <h2>Sites and upstreams</h2>
        {#if !sites.upstreamCheck}<span class="muted">The upstream check is off</span>{/if}
      </div>
      {#if rows.length === 0}
        <p class="empty">No upstreams in Caddy's configuration.</p>
      {:else}
        <ul>
          {#each visible.rows as row (row.host + row.address)}
            <li data-state={row.state}>
              <span class="mark" aria-hidden="true"></span>
              <div class="body">
                <div class="nm">{row.host || '(any host)'}</div>
                <div class="sub">{row.address}</div>
              </div>
              <div class="st">
                <div class="word">{row.state}</div>
                {#if row.state === 'answering'}
                  <div class="detail">
                    {formatMs(row.latencyMs)}{#if row.httpStatus != null && (row.httpStatus < 200 || row.httpStatus > 299)}, answers {row.httpStatus}{/if}
                  </div>
                {:else if row.state === 'down'}
                  <div class="detail">no answer{row.siblings > 1 ? `, one of ${row.siblings}` : ''}</div>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
        {#if visible.hidden > 0}
          <button class="more" type="button" onclick={() => (expanded = true)}>Show {visible.hidden} more</button>
        {:else if expanded && rows.length > SHOWN}
          <button class="more" type="button" onclick={() => (expanded = false)}>Show less</button>
        {/if}
      {/if}
    </section>
  {/if}

  <footer class="foot"><span>Admin API: {caddy.endpoint}</span></footer>
{:else if !loadError}
  <p class="empty">Loading…</p>
{/if}

<style>
  .hero {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 2rem;
  }
  h1 {
    margin: 0.35rem 0 0;
    font: 600 clamp(2.4rem, 5vw, 3.4rem) / 1.05 var(--display);
    letter-spacing: -0.03em;
  }
  h2 {
    margin: 0;
    font: 600 1.3rem / 1.2 var(--display);
    letter-spacing: -0.02em;
  }
  .eyebrow {
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 1rem;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.3rem 0.8rem;
    border: 1px solid var(--line2);
    border-radius: 999px;
    font-size: 0.85rem;
  }
  .chip .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--ok);
  }
  .chip[data-state='down'] .dot {
    background: var(--bad);
  }
  .why,
  .muted,
  .empty,
  .quiet {
    color: var(--muted);
  }
  .error {
    color: var(--bad);
  }
  .note {
    color: var(--warn);
  }

  .figures {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 1.5rem 2rem;
    padding: 1.5rem 0;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    margin-bottom: 2rem;
  }
  .figure {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    min-width: 0;
  }
  .v {
    font: 600 clamp(2rem, 4vw, 2.8rem) / 1.05 var(--display);
    letter-spacing: -0.03em;
    font-variant-numeric: tabular-nums;
  }
  .n {
    color: var(--muted);
    font-size: 0.85rem;
  }
  .figure[data-tone='bad'] .compare,
  .figure[data-tone='bad'] .n:last-child {
    color: var(--bad);
  }
  .strip {
    display: flex;
    gap: 2px;
    height: 0.5rem;
  }
  .strip i {
    flex: 1 1 0;
    min-width: 2px;
    border-radius: 2px;
    background: var(--ok);
  }
  .strip i[data-state='down'] {
    background: var(--bad);
  }
  .strip i[data-state='unknown'] {
    background: var(--faint);
  }

  .main {
    display: flex;
    flex-wrap: wrap;
    gap: 2rem;
    align-items: flex-start;
    margin-bottom: 2.5rem;
  }
  .chartcard {
    flex: 2 1 28rem;
    min-width: 0;
    padding: 1.25rem 1.5rem;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface);
  }
  .attention {
    flex: 1 1 20rem;
    min-width: 0;
    padding-top: 1rem;
    border-top: 1px solid var(--border);
  }
  .attention h2 {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .item {
    display: flex;
    gap: 0.75rem;
    padding: 1rem 0;
    border-bottom: 1px solid var(--border);
  }
  .item:last-child {
    border-bottom: 0;
  }
  .mark {
    flex: none;
    width: 0.6rem;
    height: 0.6rem;
    margin-top: 0.45rem;
    border-radius: 50%;
    background: var(--faint);
  }
  [data-severity='bad'] > .mark,
  [data-state='down'] > .mark {
    background: var(--bad);
  }
  [data-severity='warn'] > .mark {
    background: var(--warn);
  }
  [data-severity='info'] > .mark {
    background: var(--info);
  }
  [data-state='answering'] > .mark {
    background: var(--ok);
  }
  .item .t {
    font: 600 1.05rem / 1.3 var(--display);
    letter-spacing: -0.01em;
  }
  .item p {
    margin: 0.3rem 0 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .info {
    position: relative;
    display: inline-grid;
    place-items: center;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--faint);
  }
  .info:hover,
  .info:focus-visible {
    color: var(--text);
  }
  .tipbox {
    position: absolute;
    top: 1.6rem;
    left: 0;
    z-index: 2;
    display: none;
    width: 17rem;
    max-width: 80vw;
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    font: 400 0.8rem / 1.45 var(--sans);
    letter-spacing: 0;
    text-align: left;
    text-transform: none;
  }
  .info:hover .tipbox,
  .info:focus-visible .tipbox {
    display: block;
  }

  .upstreams .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.5rem;
    padding-bottom: 0.75rem;
    border-bottom: 1px solid var(--border);
  }
  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    align-items: center;
    gap: 0.9rem;
    padding: 0.85rem 0.75rem;
    border-bottom: 1px solid var(--border);
    border-radius: var(--radius);
  }
  li[data-state='down'] {
    background: var(--bad-soft);
    border-bottom-color: transparent;
  }
  li .mark {
    margin-top: 0;
  }
  .body {
    flex: 1 1 auto;
    min-width: 0;
  }
  .nm {
    overflow: hidden;
    font: 600 1.05rem / 1.2 var(--display);
    letter-spacing: -0.01em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sub {
    margin-top: 0.15rem;
    overflow: hidden;
    color: var(--muted);
    font: 400 0.8rem / 1.4 var(--mono);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .st {
    flex: none;
    text-align: right;
    font-size: 0.85rem;
  }
  li[data-state='down'] .word {
    color: var(--bad);
    font-weight: 600;
  }
  li[data-state='answering'] .word {
    color: var(--ok);
  }
  li[data-state='unknown'] .word {
    color: var(--muted);
  }
  .detail {
    color: var(--muted);
    font: 400 0.75rem / 1.4 var(--mono);
  }
  .more {
    margin-top: 0.75rem;
    padding: 0.25rem 0;
    border: 0;
    border-bottom: 1px solid var(--accent);
    border-radius: 0;
    background: transparent;
    color: var(--text);
  }
  .foot {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 0.5rem;
    margin-top: 3rem;
    padding-top: 1rem;
    border-top: 1px solid var(--border);
    color: var(--faint);
    font: 400 0.75rem / 1.4 var(--mono);
  }
  @media (max-width: 36rem) {
    .nm,
    .sub {
      overflow-wrap: anywhere;
      white-space: normal;
    }
  }
</style>
