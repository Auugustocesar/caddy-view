<script>
  import { formatBytes, formatCount, formatMs } from '../lib/format.js'

  /** @type {{hosts: Array<{host: string, requests: number, bytes: number, errors5xx: number, avgMs: number|null, p95Ms: number|null}>, selected?: string, onselect?: (host: string) => void}} */
  let { hosts, selected = '', onselect = () => {} } = $props()
</script>

{#if hosts.length === 0}
  <p class="empty">No traffic in this period</p>
{:else}
  <div class="scroll">
    <table aria-label="Hosts">
      <thead>
        <tr>
          <th>Host</th><th class="num">Requests</th><th class="num">Traffic</th><th class="num">Server errors</th><th class="num">Avg. time</th><th class="num">p95</th>
        </tr>
      </thead>
      <tbody>
        {#each hosts as h (h.host)}
          <tr data-errors={h.errors5xx > 0 ? 'true' : undefined}>
            <td class="host">
              {#if h.host === ''}
                (no host)
              {:else}
                <button type="button" aria-pressed={selected === h.host} onclick={() => onselect(selected === h.host ? '' : h.host)}>{h.host}</button>
              {/if}
            </td>
            <td class="num">{formatCount(h.requests)}</td>
            <td class="num">{formatBytes(h.bytes)}</td>
            <td class="num errors">{formatCount(h.errors5xx)}</td>
            <td class="num">{formatMs(h.avgMs)}</td>
            <td class="num">{formatMs(h.p95Ms)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .empty {
    margin: 1rem 0;
    color: var(--muted);
    text-align: center;
  }
  .scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    padding: 0.55rem 0.75rem;
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
  .host button {
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font-weight: 600;
    text-decoration: underline;
    text-decoration-color: var(--border);
    text-underline-offset: 0.25em;
  }
  .host button:hover {
    text-decoration-color: var(--accent);
  }
  .host button[aria-pressed='true'] {
    color: var(--accent);
    text-decoration-color: var(--accent);
  }
  tr[data-errors='true'] .errors {
    color: var(--bad);
    font-weight: 600;
  }
</style>
