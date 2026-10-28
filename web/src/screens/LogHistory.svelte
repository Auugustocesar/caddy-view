<script>
  import { getHosts, getLogs } from '../lib/api.js'
  import { formatBytes, formatMs, formatStamp } from '../lib/format.js'
  import { PERIODS, periodById, periodRange } from '../lib/metrics.js'

  /** @type {{now?: () => Date, searchDelay?: number}} */
  let { now = () => new Date(), searchDelay = 300 } = $props()

  const PAGE = 50
  // A status class is a range of codes.
  const CLASS_RANGES = { '2xx': [200, 299], '3xx': [300, 399], '4xx': [400, 499], '5xx': [500, 599] }

  let periodId = $state('24h')
  let host = $state('')
  let statusClass = $state('')
  let text = $state('')

  /** @type {'loading'|'ready'|'off'|'failed'} */
  let phase = $state('loading')
  let error = $state('')
  /** @type {Array<any>} */
  let entries = $state([])
  /** @type {number | null} */
  let next = $state(null)
  let loadingMore = $state(false)
  /** @type {string[]} */
  let hostNames = $state([])

  const options = $derived(host && !hostNames.includes(host) ? [...hostNames, host] : hostNames)

  /** The filters as the API takes them. */
  function filters() {
    const [minStatus, maxStatus] = CLASS_RANGES[statusClass] ?? []
    return { from: periodRange(periodById(periodId), now()).from, host, minStatus, maxStatus, q: text, limit: PAGE }
  }

  // Only the answer to the latest search counts. "More" belongs to the search it was asked in.
  let searchSeq = 0

  function fail(err) {
    // Without a history the routes are not there at all: that is a setting, not a failure.
    if (err.status === 404) phase = 'off'
    else {
      phase = 'failed'
      error = err.message
    }
  }

  async function search() {
    const id = ++searchSeq
    loadingMore = false
    try {
      const answer = await getLogs(filters())
      if (id !== searchSeq) return
      entries = answer.entries
      next = answer.next
      phase = 'ready'
      error = ''
    } catch (err) {
      if (id === searchSeq) fail(err)
    }
  }

  async function more() {
    if (loadingMore || next === null) return
    const id = searchSeq
    loadingMore = true
    try {
      const answer = await getLogs({ ...filters(), before: next })
      if (id !== searchSeq) return
      entries = [...entries, ...answer.entries]
      next = answer.next
    } catch (err) {
      if (id === searchSeq) fail(err)
    } finally {
      if (id === searchSeq) loadingMore = false
    }
  }

  let hostsSeq = 0
  async function loadHosts() {
    const id = ++hostsSeq
    const range = periodRange(periodById(periodId), now())
    try {
      const answer = await getHosts('hour', { from: range.from, to: range.to })
      if (id === hostsSeq) hostNames = answer.hosts.map((h) => h.host).filter((name) => name !== '')
    } catch {
      // The hosts only fill the select; the search works without them.
      if (id === hostsSeq) hostNames = []
    }
  }

  search()
  loadHosts()

  let timer
  function typed() {
    clearTimeout(timer)
    timer = setTimeout(search, searchDelay)
  }

  function changed() {
    clearTimeout(timer)
    search()
  }

  function changedPeriod() {
    changed()
    loadHosts()
  }

  const classOf = (status) => (status >= 100 && status <= 599 ? `${Math.floor(status / 100)}xx` : 'other')
</script>

{#if phase === 'off'}
  <p class="note">Traffic history is off, so there are no stored requests to search. Start caddy-view with its history on.</p>
{:else}
  <div class="filters">
    <label class="field search">
      <span>Search path</span>
      <input type="search" placeholder="/api/…" bind:value={text} oninput={typed} />
    </label>
    <label class="field">
      <span>Host</span>
      <select bind:value={host} onchange={changed}>
        <option value="">All hosts</option>
        {#each options as name (name)}
          <option value={name}>{name}</option>
        {/each}
      </select>
    </label>
    <label class="field">
      <span>Status</span>
      <select bind:value={statusClass} onchange={changed}>
        <option value="">Any status</option>
        {#each Object.keys(CLASS_RANGES) as key}
          <option value={key}>{key}</option>
        {/each}
      </select>
    </label>
    <label class="field">
      <span>Period</span>
      <select bind:value={periodId} onchange={changedPeriod}>
        {#each PERIODS as p (p.id)}
          <option value={p.id}>{p.label}</option>
        {/each}
      </select>
    </label>
  </div>

  {#if phase === 'failed'}
    <p class="error" role="alert">{error}</p>
  {:else if phase === 'loading'}
    <p class="empty">Loading…</p>
  {:else if entries.length === 0}
    <p class="empty">No requests match</p>
  {:else}
    <div class="card scroll">
      <table aria-label="Requests">
        <thead>
          <tr>
            <th>Time</th><th>Status</th><th>Method</th><th>Host</th><th>Path</th><th class="num">Took</th><th class="num">Size</th><th>Client</th>
          </tr>
        </thead>
        <tbody>
          {#each entries as entry}
            <tr>
              <td class="time">{formatStamp(entry.time)}</td>
              <td class="st"><span class="code" data-status={classOf(entry.status)}>{entry.status}</span></td>
              <td class="method">{entry.method}</td>
              <td class="host">{entry.host}</td>
              <td class="path">{entry.path}</td>
              <td class="num took">{formatMs(entry.durationMs)}</td>
              <td class="num size">{formatBytes(entry.bytes)}</td>
              <td class="client">{entry.clientIp}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if next !== null}
      <div class="more"><button type="button" onclick={more} disabled={loadingMore}>Load more</button></div>
    {/if}
  {/if}
{/if}

<style>
  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .field {
    display: grid;
    gap: 0.2rem;
    color: var(--muted);
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .field input,
  .field select {
    padding: 0.4rem 0.7rem;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 0.9rem;
    letter-spacing: normal;
    text-transform: none;
  }
  .search {
    flex: 1;
    min-width: 12rem;
  }
  .card {
    border-radius: var(--radius-lg);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .scroll {
    padding: 0.25rem 0.5rem;
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  th,
  td {
    padding: 0.45rem 0.65rem;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  th {
    color: var(--muted);
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }
  .num {
    text-align: right;
  }
  .time {
    color: var(--muted);
  }
  .path {
    max-width: 24rem;
    overflow: hidden;
    text-overflow: ellipsis;
    font-family: var(--mono);
  }
  .code {
    font-family: var(--mono);
    font-weight: 600;
  }
  .code[data-status='2xx'] {
    color: var(--ok);
  }
  .code[data-status='4xx'] {
    color: var(--warn);
  }
  .code[data-status='5xx'] {
    color: var(--bad);
  }
  /* On a phone a request is a card of three lines: what it was and where, when and for which host, and how it went. */
  @media (max-width: 40rem) {
    thead {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
    }
    table,
    tbody {
      display: block;
    }
    tr {
      display: grid;
      grid-template-columns: auto auto 1fr;
      grid-template-areas:
        'st method path'
        'time time host'
        'took size client';
      gap: 0.15rem 0.75rem;
      padding: 0.6rem 0.4rem;
      border-bottom: 1px solid var(--border);
    }
    tbody tr:last-child {
      border-bottom: 0;
    }
    td {
      padding: 0;
      border: 0;
      white-space: normal;
    }
    .st { grid-area: st; }
    .method { grid-area: method; }
    .time { grid-area: time; font-size: 0.8rem; }
    .host { grid-area: host; text-align: right; }
    .took { grid-area: took; text-align: left; color: var(--muted); font-size: 0.8rem; }
    .size { grid-area: size; text-align: left; color: var(--muted); font-size: 0.8rem; }
    .client { grid-area: client; text-align: right; color: var(--muted); font-size: 0.8rem; }
    .path {
      grid-area: path;
      max-width: none;
      overflow: visible;
      overflow-wrap: anywhere;
    }
  }
  .more {
    margin-top: 0.75rem;
    text-align: center;
  }
  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
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
