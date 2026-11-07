<script>
  import LineChart from '../components/LineChart.svelte'
  import HostsTable from '../components/HostsTable.svelte'
  import Segmented from '../components/Segmented.svelte'
  import Tile from '../components/Tile.svelte'
  import { getAttention, getHosts, getTraffic } from '../lib/api.js'
  import { compare, describeItems } from '../lib/attention.js'
  import { formatBytes, formatCount, formatMs, formatPercent } from '../lib/format.js'
  import { PERIODS, chartTitle, periodById, periodRange, summarize } from '../lib/metrics.js'

  /** @type {{now?: () => Date}} */
  let { now = () => new Date() } = $props()

  const periodOptions = PERIODS.map((p) => ({ value: p.id, label: p.id }))

  let periodId = $state('24h')
  let host = $state('')
  /** @type {'loading'|'ready'|'off'|'failed'} */
  let phase = $state('loading')
  let error = $state('')
  /** @type {{resolution: string, buckets: Array<any> | null} | null} */
  let traffic = $state(null)
  /** @type {Array<any> | null} null when the hosts could not be read */
  let hosts = $state(null)

  /** @type {{buckets: Array<any> | null} | null} the period of the same length right before, null when it cannot be read */
  let previousTraffic = $state(null)
  /** @type {Array<any> | null} null when it cannot be read, or the period is not one the server answers */
  let attention = $state(null)
  // The periods /api/attention answers; a month is longer than it looks back.
  const ATTENTION_WINDOWS = new Set(['1h', '24h', '7d'])
  let attentionSeq = 0

  async function loadAttention() {
    const id = ++attentionSeq
    if (!ATTENTION_WINDOWS.has(periodId)) {
      attention = null
      return
    }
    try {
      const answer = await getAttention(periodId)
      if (id === attentionSeq) attention = answer.items
    } catch {
      // It is a help, not the page: without it the figures still show.
      if (id === attentionSeq) attention = null
    }
  }

  const period = $derived(periodById(periodId))
  const items = $derived(attention ? describeItems(attention) : null)
  // The hosts the items name, for the table. A host with errors shows as that, whatever else is said of it.
  const flags = $derived.by(() => {
    const out = {}
    for (const item of attention ?? []) {
      if (item.kind === 'errors_up') out[item.host] = 'errors'
      else if (item.kind === 'host_slow' && !out[item.host]) out[item.host] = 'slow'
    }
    return out
  })
  const sum = $derived(summarize(traffic?.buckets))
  const prev = $derived(previousTraffic ? summarize(previousTraffic.buckets) : null)
  const requestsCompare = $derived(prev ? compare(sum.requests, prev.requests) : null)
  const errorsCompare = $derived(prev ? compare(sum.errorRate, prev.errorRate, { times: true, worseWhenHigher: true }) : null)
  // The host that is chosen stays in the list even when this period has no traffic from it.
  const hostNames = $derived.by(() => {
    const names = (hosts ?? []).map((h) => h.host).filter((name) => name !== '')
    return host && !names.includes(host) ? [...names, host] : names
  })

  // Only the answer to the latest question counts: a slower answer to an older one is dropped.
  let trafficSeq = 0
  let hostsSeq = 0

  async function loadTraffic(range = periodRange(period, now())) {
    const id = ++trafficSeq
    try {
      const answer = await getTraffic(range.resolution, { from: range.from, to: range.to, host })
      if (id !== trafficSeq) return
      traffic = answer
      phase = 'ready'
      error = ''
      loadPrevious(range, id)
    } catch (err) {
      if (id !== trafficSeq) return
      // Without a history the routes are not there at all: that is a setting, not a failure.
      if (err.status === 404) phase = 'off'
      else {
        phase = 'failed'
        error = err.message
      }
    }
  }

  // The period before ends where this one starts, and is as long. It only completes the picture.
  async function loadPrevious(range, id) {
    const span = new Date(range.to).getTime() - new Date(range.from).getTime()
    const from = new Date(new Date(range.from).getTime() - span).toISOString().replace(/\.\d{3}Z$/, 'Z')
    try {
      const answer = await getTraffic(range.resolution, { from, to: range.from, host })
      if (id === trafficSeq) previousTraffic = answer
    } catch {
      if (id === trafficSeq) previousTraffic = null
    }
  }

  async function loadHosts(range) {
    const id = ++hostsSeq
    try {
      const answer = await getHosts(range.resolution, { from: range.from, to: range.to })
      if (id === hostsSeq) hosts = answer.hosts
    } catch {
      // The hosts only complete the picture; the tiles and the chart do not wait for them.
      if (id === hostsSeq) hosts = null
    }
  }

  function loadAll() {
    const range = periodRange(period, now())
    loadTraffic(range)
    loadHosts(range)
    loadAttention()
  }

  loadAll()

  function changePeriod(id) {
    periodId = id
    loadAll()
  }

  function changeHost(name) {
    host = name
    loadTraffic()
  }
</script>

<header class="pagehead">
  <div>
    <div class="eyebrow">From the access log, compared with the previous {period.id === '1h' ? 'hour' : period.id === '24h' ? '24 hours' : period.id === '7d' ? '7 days' : '30 days'}</div>
    <h1>Metrics</h1>
  </div>
  {#if phase !== 'off'}
    <Segmented label="Period" options={periodOptions} value={periodId} onchange={changePeriod} />
  {/if}
</header>

{#if phase === 'off'}
  <p class="note">Traffic history is off, so there is nothing to show here. Start caddy-view with its history on to see traffic.</p>
{:else}
  <div class="controls">
    <label class="host-filter">
      <span>Host</span>
      <select value={host} onchange={(event) => changeHost(event.currentTarget.value)}>
        <option value="">All hosts</option>
        {#each hostNames as name (name)}
          <option value={name}>{name}</option>
        {/each}
      </select>
    </label>
    <button type="button" onclick={loadAll}>Refresh</button>
  </div>

  {#if phase === 'failed'}
    <p class="error" role="alert">{error}</p>
  {:else if phase === 'loading'}
    <p class="empty">Loading…</p>
  {:else}
    <div class="tiles">
      <Tile label="Requests" value={formatCount(sum.requests)} detail={requestsCompare?.text ?? ''} />
      <Tile label="Data sent" value={formatBytes(sum.bytes)} detail={sum.requests > 0 ? `${formatBytes(sum.bytes / sum.requests)} per request` : ''} />
      <Tile
        label="5xx rate"
        value={formatPercent(sum.errorRate)}
        detail={`${sum.errors5xx} of ${formatCount(sum.requests)} requests`}
        extra={errorsCompare?.text ?? ''}
        tone={errorsCompare?.tone ?? (sum.errors5xx > 0 ? 'warn' : undefined)}
      />
      <Tile label="Avg. response time" value={formatMs(sum.avgMs)} detail={sum.maxMs === null ? '' : `max ${formatMs(sum.maxMs)}`} />
    </div>

    <div class="main">
      <div class="card chart">
        <LineChart
          title={chartTitle(traffic?.resolution ?? period.resolution)}
          buckets={traffic?.buckets ?? []}
          previous={previousTraffic?.buckets ?? null}
          labels={period.hours > 24 ? 'date' : 'time'}
        />
      </div>

      {#if items}
        <section class="attention" aria-label="Needs attention">
          <h2>Needs attention</h2>
          {#each items as item (item.key)}
            <div class="item" data-severity={item.severity}>
              <span class="mark" aria-hidden="true"></span>
              <div>
                <div class="t">{item.title}</div>
                <p>{item.text}</p>
              </div>
            </div>
          {:else}
            <p class="empty">Nothing needs attention.</p>
          {/each}
        </section>
      {/if}
    </div>

    <section class="card flush" aria-labelledby="hosts-heading">
      <h2 id="hosts-heading">Hosts</h2>
      {#if hosts === null}
        <p class="empty">The hosts could not be read.</p>
      {:else}
        <HostsTable {hosts} selected={host} {flags} onselect={changeHost} />
      {/if}
    </section>
  {/if}
{/if}

<style>
  .pagehead {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1.5rem;
  }
  .eyebrow {
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  h1 {
    margin: 0.35rem 0 0;
    font: 600 clamp(2.2rem, 4.5vw, 3rem) / 1.05 var(--display);
    letter-spacing: -0.03em;
  }
  h2 {
    margin: 0 0 0.75rem;
    font: 600 1.3rem / 1.2 var(--display);
    letter-spacing: -0.02em;
  }
  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 1.5rem;
  }
  .host-filter {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--muted);
    font-size: 0.85rem;
  }
  .host-filter select {
    padding: 0.35rem 0.7rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 1.5rem 2rem;
    margin-bottom: 2rem;
  }
  .main {
    display: flex;
    flex-wrap: wrap;
    gap: 2.5rem;
    align-items: flex-start;
  }
  .chart {
    flex: 2 1 28rem;
    min-width: 0;
    margin-top: 0;
  }
  .card {
    margin-top: 2rem;
    padding: 1.25rem 1.5rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius-lg);
    background: linear-gradient(155deg, var(--accent-soft), transparent 52%), var(--surface);
  }
  /* the hosts: a title over a table, with no box around them */
  .card.flush {
    padding: 0;
    border: 0;
    border-radius: 0;
    background: transparent;
  }
  .card.flush h2 {
    margin-bottom: 0.25rem;
  }
  .attention {
    flex: 1 1 20rem;
    min-width: 0;
  }
  .attention h2 {
    margin: 0;
    padding-top: 0.9rem;
    border-top: 1px solid var(--line2);
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
  [data-severity='bad'] > .mark {
    background: var(--bad);
  }
  [data-severity='warn'] > .mark {
    background: var(--warn);
  }
  [data-severity='info'] > .mark {
    background: var(--info);
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
  .empty {
    color: var(--muted);
  }
  .error {
    color: var(--bad);
  }
  .note {
    color: var(--warn);
  }
</style>
