<script>
  import { formatCount, formatPercent } from '../lib/format.js'
  import { statusShares } from '../lib/metrics.js'

  /** @type {{status: Record<string, number> | null}} */
  let { status } = $props()

  const shares = $derived(statusShares(status))
  const description = $derived(`Status codes: ${shares.map((s) => `${s.label} ${formatPercent(s.share)}`).join(', ')}`)
</script>

{#if shares.length === 0}
  <p class="empty">No requests</p>
{:else}
  <div class="bar" role="img" aria-label={description}>
    {#each shares as share (share.key)}
      <span data-class={share.key} style:width="{(share.share * 100).toFixed(2)}%"></span>
    {/each}
  </div>
  <table aria-label="Status codes">
    <thead>
      <tr><th>Class</th><th>Meaning</th><th class="num">Requests</th><th class="num">Share</th></tr>
    </thead>
    <tbody>
      {#each shares as share (share.key)}
        <tr data-class={share.key}>
          <td><span class="dot" aria-hidden="true"></span>{share.key}</td>
          <td>{share.label}</td>
          <td class="num">{formatCount(share.count)}</td>
          <td class="num">{formatPercent(share.share)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}

<style>
  .empty {
    margin: 1rem 0;
    color: var(--muted);
    text-align: center;
  }
  .bar {
    display: flex;
    height: 0.9rem;
    overflow: hidden;
    border-radius: 999px;
    background: var(--surface-2);
  }
  .bar span {
    min-width: 3px;
  }
  [data-class='1xx'],
  [data-class='other'] {
    --class-color: var(--muted);
  }
  [data-class='2xx'] {
    --class-color: var(--ok);
  }
  [data-class='3xx'] {
    --class-color: var(--accent);
  }
  [data-class='4xx'] {
    --class-color: var(--warn);
  }
  [data-class='5xx'] {
    --class-color: var(--bad);
  }
  .bar span {
    background: var(--class-color);
  }
  table {
    width: 100%;
    margin-top: 0.75rem;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    padding: 0.3rem 0.5rem 0.3rem 0;
    text-align: left;
    font-variant-numeric: tabular-nums;
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
  .dot {
    display: inline-block;
    width: 0.6rem;
    height: 0.6rem;
    margin-right: 0.5rem;
    border-radius: 50%;
    background: var(--class-color);
  }
</style>
