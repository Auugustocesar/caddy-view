<script>
  import { formatBytes, formatCount, formatMs, formatPercent } from '../lib/format.js'
  import { statusShares } from '../lib/metrics.js'

  /**
   * `flags` marks hosts that need attention: 'errors' for more 5xx than before, 'slow' for a p95 far above the others.
   * @type {{hosts: Array<{host: string, requests: number, bytes: number, errors5xx: number, avgMs: number|null, p95Ms: number|null}>, selected?: string, flags?: Record<string, 'errors'|'slow'>, onselect?: (host: string) => void}}
   */
  let { hosts, selected = '', flags = {}, onselect = () => {} } = $props()
</script>

{#if hosts.length === 0}
  <p class="empty">No traffic in this period</p>
{:else}
  <div class="scroll">
    <table aria-label="Hosts">
      <thead>
        <tr>
          <th>Host</th><th class="num">Requests</th><th class="num">p95</th><th class="num">5xx</th><th class="num">Sent</th><th>Status codes</th>
        </tr>
      </thead>
      <tbody>
        {#each hosts as h (h.host)}
          <tr data-errors={h.errors5xx > 0 ? 'true' : undefined} data-flag={flags[h.host]}>
            <td class="host">
              {#if h.host === ''}
                (no host)
              {:else}
                <button type="button" aria-pressed={selected === h.host} onclick={() => onselect(selected === h.host ? '' : h.host)}>{h.host}</button>
              {/if}
            </td>
            <td class="num">{formatCount(h.requests)}</td>
            <td class="num p95">{formatMs(h.p95Ms)}</td>
            <td class="num errors">{formatCount(h.errors5xx)}</td>
            <td class="num">{formatBytes(h.bytes)}</td>
            <td class="codes">
              {#if statusShares(h.status).length > 0}
                {@const shares = statusShares(h.status)}
                <div class="bar" role="img" aria-label={shares.map((x) => `${formatPercent(x.share)} ${x.key}`).join(', ')}>
                  {#each shares as x (x.key)}<span data-class={x.key} style:width="{(x.share * 100).toFixed(2)}%"></span>{/each}
                </div>
              {/if}
            </td>
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
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
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
  .codes {
    width: 9rem;
  }
  .bar {
    display: flex;
    height: 0.5rem;
    gap: 1px;
    overflow: hidden;
    border-radius: 0.25rem;
  }
  .bar span {
    display: block;
    min-width: 2px;
    background: var(--ok);
  }
  .bar span[data-class='3xx'] {
    background: var(--info);
  }
  .bar span[data-class='4xx'] {
    background: var(--warn);
  }
  .bar span[data-class='5xx'] {
    background: var(--bad);
  }
  .bar span[data-class='1xx'],
  .bar span[data-class='other'] {
    background: var(--faint);
  }
  tr[data-errors='true'] .errors {
    color: var(--bad);
    font-weight: 600;
  }
  tr[data-flag='errors'] .errors {
    color: var(--bad);
    font-weight: 600;
  }
  tr[data-flag='slow'] .p95 {
    color: var(--warn);
    font-weight: 600;
  }
</style>
