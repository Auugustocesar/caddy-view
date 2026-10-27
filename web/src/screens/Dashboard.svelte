<script>
  /**
   * @type {{
   *   status?: {caddy: {reachable: boolean, endpoint: string, error?: string}} | null,
   *   sites?: {sites: Array<{server: string, host: string, upstreams: Array<{address: string, numRequests: number, fails: number}>}>, upstreamsAvailable: boolean} | null,
   *   loadError?: string,
   * }}
   */
  let { status = null, sites = null, loadError = '' } = $props()

  const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`
</script>

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

  {#if caddy.reachable && sites}
    {#if !sites.upstreamsAvailable}
      <p class="note">Upstream health is unavailable right now, so only the addresses are shown.</p>
    {/if}

    {#if sites.sites.length === 0}
      <p class="empty">No sites found</p>
    {:else}
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
    {/if}
  {/if}
{:else if !loadError}
  <p class="empty">Loading…</p>
{/if}

<style>
  .banner {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 1rem;
    padding: 0.75rem 1rem;
    border-radius: var(--radius);
    border: 1px solid var(--border);
    border-left-width: 4px;
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
  table {
    width: 100%;
    border-collapse: collapse;
    margin-top: 1rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 0.85rem;
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
