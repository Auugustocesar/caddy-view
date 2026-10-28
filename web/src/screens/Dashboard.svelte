<script>
  import BarChart from '../components/BarChart.svelte'
  import Tile from '../components/Tile.svelte'
  import { formatBytes, formatCount, formatMs, formatPercent } from '../lib/format.js'

  /**
   * @type {{
   *   status?: {caddy: {reachable: boolean, endpoint: string, error?: string}} | null,
   *   sites?: {sites: Array<{server: string, host: string, upstreams: Array<{address: string, numRequests: number, fails: number}>}>, upstreamsAvailable: boolean} | null,
   *   summary?: {requests: number, errors5xx: number, errorRate: number|null, avgMs: number|null, p95Ms: number|null, bytes: number, inFlight: number|null, upstreams: {healthy: number, total: number}|null} | null,
   *   traffic?: {buckets: Array<{start: string, requests: number}>|null} | null,
   *   historyOff?: boolean,
   *   loadError?: string,
   * }}
   */
  let { status = null, sites = null, summary = null, traffic = null, historyOff = false, loadError = '' } = $props()

  const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`
</script>

<header class="page">
  <h1>Dashboard</h1>
  <p class="subtitle">The last 24 hours and your sites at a glance.</p>
</header>

{#if loadError}
  <p class="error" role="alert">{loadError}</p>
{/if}

{#if status}
  {@const caddy = status.caddy}
  <p class="banner" role="status" data-state={caddy.reachable ? 'up' : 'down'}>
    {#if caddy.reachable}
      <strong>Caddy is reachable</strong>
    {:else}
      <strong>Caddy is not reachable</strong>
      {#if caddy.error}<span class="detail">{caddy.error}</span>{/if}
    {/if}
    <span class="detail">Admin API: {caddy.endpoint}</span>
  </p>

  {#if historyOff}
    <p class="note">Traffic history is off, so there are no totals or charts. Start caddy-view with persistence to see them.</p>
  {:else if summary}
    <section aria-labelledby="last-24-hours">
      <h2 id="last-24-hours">Last 24 hours</h2>
      <div class="tiles">
        <Tile label="Requests" value={formatCount(summary.requests)} detail={summary.inFlight == null ? '' : `${summary.inFlight} in flight`} />
        <Tile label="Traffic" value={formatBytes(summary.bytes)} />
        <Tile
          label="Server errors"
          value={formatPercent(summary.errorRate)}
          detail={plural(summary.errors5xx, 'request')}
          tone={summary.errors5xx > 0 ? 'bad' : undefined}
        />
        <Tile
          label="Avg. response time"
          value={formatMs(summary.avgMs)}
          detail={summary.p95Ms == null ? '' : `p95 ${formatMs(summary.p95Ms)}`}
        />
        {#if summary.upstreams}
          <Tile
            label="Upstreams"
            value={`${summary.upstreams.healthy} of ${summary.upstreams.total}`}
            detail="healthy"
            tone={summary.upstreams.healthy < summary.upstreams.total ? 'warn' : undefined}
          />
        {/if}
      </div>
      {#if traffic}
        <div class="card">
          <BarChart title="Requests per hour" buckets={traffic.buckets ?? []} />
        </div>
      {/if}
    </section>
  {/if}

  {#if caddy.reachable && sites}
    <section aria-labelledby="running-sites">
      <h2 id="running-sites">Sites in Caddy</h2>
      {#if !sites.upstreamsAvailable}
        <p class="note">Upstream health is unavailable right now, so only the addresses are shown.</p>
      {/if}

      {#if sites.sites.length === 0}
        <p class="empty">No sites found</p>
      {:else}
        <div class="card flush">
          <table>
            <thead>
              <tr><th>Site</th><th>Server</th><th>Upstreams</th></tr>
            </thead>
            <tbody>
              {#each sites.sites as site}
                <tr>
                  <td class="host">{site.host || '(any host)'}</td>
                  <td>{site.server}</td>
                  <td>
                    {#each site.upstreams as upstream}
                      <div class="upstream" data-health={sites.upstreamsAvailable && upstream.fails > 0 ? 'failing' : 'ok'}>
                        <code>{upstream.address}</code>
                        {#if sites.upstreamsAvailable}
                          <span>{upstream.numRequests} active</span>
                          <span>{plural(upstream.fails, 'failure')}</span>
                        {/if}
                      </div>
                    {:else}
                      <span class="detail">No upstream</span>
                    {/each}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  {/if}
{:else if !loadError}
  <p class="empty">Loading…</p>
{/if}

<style>
  .page {
    margin-bottom: 1.5rem;
  }
  h1 {
    margin: 0;
    font-size: 2.1rem;
    font-weight: 300;
    letter-spacing: -0.02em;
  }
  .subtitle {
    margin: 0.25rem 0 0;
    color: var(--muted);
  }
  h2 {
    margin: 2rem 0 0.75rem;
    font-size: 1.05rem;
    font-weight: 500;
  }
  .banner {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 1rem;
    padding: 0.75rem 1rem;
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow);
    border-left: 4px solid var(--border);
  }
  .banner[data-state='up'] {
    border-left-color: var(--ok);
  }
  .banner[data-state='down'] {
    border-left-color: var(--bad);
  }
  .detail,
  .empty {
    color: var(--muted);
  }
  .error {
    color: var(--bad);
  }
  .note {
    color: var(--warn);
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
    margin-top: 0;
    padding: 0.25rem 0.5rem;
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    text-align: left;
    padding: 0.65rem 0.75rem;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 0.75rem;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }
  .host {
    font-weight: 600;
  }
  .upstream {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.75rem;
    align-items: baseline;
  }
  .upstream[data-health='failing'] {
    color: var(--bad);
  }
  .upstream span {
    color: var(--muted);
    font-size: 0.85rem;
  }
  .upstream[data-health='failing'] span {
    color: inherit;
  }
</style>
