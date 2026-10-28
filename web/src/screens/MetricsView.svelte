<script>
  import BarChart from '../components/BarChart.svelte'
  import HostsTable from '../components/HostsTable.svelte'
  import Segmented from '../components/Segmented.svelte'
  import StatusBar from '../components/StatusBar.svelte'
  import Tile from '../components/Tile.svelte'
  import { getHosts, getTraffic } from '../lib/api.js'
  import { formatBytes, formatCount, formatMs, formatPercent } from '../lib/format.js'
  import { PERIODS, chartTitle, periodById, periodRange, summarize } from '../lib/metrics.js'

  /** @type {{now?: () => Date}} */
  let { now = () => new Date() } = $props()

  const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`
  const periodOptions = PERIODS.map((p) => ({ value: p.id, label: p.label }))

  let periodId = $state('24h')
  let host = $state('')
  /** @type {'loading'|'ready'|'off'|'failed'} */
  let phase = $state('loading')
  let error = $state('')
  /** @type {{resolution: string, buckets: Array<any> | null} | null} */
  let traffic = $state(null)
  /** @type {Array<any> | null} null when the hosts could not be read */
  let hosts = $state(null)

  const period = $derived(periodById(periodId))
  const sum = $derived(summarize(traffic?.buckets))
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

<header class="page">
  <h1>Metrics</h1>
  <p class="subtitle">Requests, status codes and response times from Caddy's access log.</p>
</header>

{#if phase === 'off'}
  <p class="note">Traffic history is off, so there is nothing to show here. Start caddy-view with its history on to see traffic.</p>
{:else}
  <div class="controls">
    <Segmented label="Period" options={periodOptions} value={periodId} onchange={changePeriod} />
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
      <Tile label="Requests" value={formatCount(sum.requests)} />
      <Tile label="Traffic" value={formatBytes(sum.bytes)} />
      <Tile
        label="Server errors"
        value={formatPercent(sum.errorRate)}
        detail={plural(sum.errors5xx, 'request')}
        tone={sum.errors5xx > 0 ? 'bad' : undefined}
      />
      <Tile label="Avg. response time" value={formatMs(sum.avgMs)} detail={sum.maxMs === null ? '' : `max ${formatMs(sum.maxMs)}`} />
    </div>

    <div class="card">
      <BarChart title={chartTitle(traffic?.resolution ?? period.resolution)} buckets={traffic?.buckets ?? []} labels={period.hours > 24 ? 'date' : 'time'} />
    </div>

    <section class="card" aria-labelledby="status-heading">
      <h2 id="status-heading">Status codes</h2>
      <StatusBar status={sum.status} />
    </section>

    <section class="card flush" aria-labelledby="hosts-heading">
      <h2 id="hosts-heading">Hosts</h2>
      {#if hosts === null}
        <p class="empty">The hosts could not be read.</p>
      {:else}
        <HostsTable {hosts} selected={host} onselect={changeHost} />
      {/if}
    </section>
  {/if}
{/if}

<style>
  .page {
    margin-bottom: 1.25rem;
  }
  h1 {
    margin: 0;
    font-size: 2.1rem;
    font-weight: 300;
    letter-spacing: -0.02em;
  }
  h2 {
    margin: 0 0 0.75rem;
    font-size: 1rem;
    font-weight: 500;
  }
  .subtitle {
    margin: 0.25rem 0 0;
    color: var(--muted);
  }
  .controls {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 1rem;
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
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(9.5rem, 1fr));
    gap: 0.75rem;
  }
  .card {
    margin-top: 0.75rem;
    padding: 1.1rem 1.25rem;
    border-radius: var(--radius-lg);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .card.flush {
    padding-bottom: 0.5rem;
  }
  .card.flush h2 {
    margin-bottom: 0.25rem;
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
