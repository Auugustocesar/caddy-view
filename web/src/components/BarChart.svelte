<script>
  import { niceMax } from '../lib/chart.js'
  import { formatClock, formatCount } from '../lib/format.js'

  /** @type {{title: string, buckets: Array<{start: string, requests: number}>}} */
  let { title, buckets } = $props()

  const HEIGHT = 220
  const PAD = { left: 48, right: 8, top: 10, bottom: 26 }
  const LABEL_GAP = 52 // the least room, in pixels, between two time labels

  let measured = $state(0)
  // Before the first measurement (and in a test, where nothing has a size) the chart has a sensible width.
  const width = $derived(measured || 640)

  const total = $derived(buckets.reduce((sum, b) => sum + b.requests, 0))
  const top = $derived(niceMax(Math.max(0, ...buckets.map((b) => b.requests))))
  const plotWidth = $derived(width - PAD.left - PAD.right)
  const plotHeight = HEIGHT - PAD.top - PAD.bottom
  const slot = $derived(plotWidth / Math.max(buckets.length, 1))
  const labelEvery = $derived(Math.max(1, Math.ceil(LABEL_GAP / slot)))
  const ticks = $derived([0, top / 2, top])
  const yOf = (value) => PAD.top + plotHeight - (value / top) * plotHeight
</script>

<section class="chart">
  <h3>{title}</h3>
  {#if total === 0}
    <p class="empty">No traffic in this period</p>
  {:else}
    <div class="frame" bind:clientWidth={measured}>
      <svg {width} height={HEIGHT} viewBox="0 0 {width} {HEIGHT}" role="img" aria-label="{title}, {formatCount(total)} requests in total">
        {#each ticks as tick}
          <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={yOf(tick)} y2={yOf(tick)} />
          <text class="axis" x={PAD.left - 8} y={yOf(tick)} text-anchor="end" dominant-baseline="middle">{formatCount(tick)}</text>
        {/each}
        {#each buckets as bucket, i}
          {@const barWidth = Math.max(1, slot * 0.68)}
          {@const x = PAD.left + i * slot + (slot - barWidth) / 2}
          <rect data-bar class="bar" {x} y={yOf(bucket.requests)} width={barWidth} height={(bucket.requests / top) * plotHeight} rx={Math.min(3, barWidth / 2)}>
            <title>{formatClock(bucket.start)}: {formatCount(bucket.requests)} requests</title>
          </rect>
          {#if i % labelEvery === 0}
            <text class="axis" x={x + barWidth / 2} y={HEIGHT - 8} text-anchor="middle">{formatClock(bucket.start)}</text>
          {/if}
        {/each}
      </svg>
    </div>
    <details>
      <summary>As table</summary>
      <table>
        <caption>{title}</caption>
        <thead><tr><th scope="col">Time</th><th scope="col">Requests</th></tr></thead>
        <tbody>
          {#each buckets as bucket}
            <tr><td>{formatClock(bucket.start)}</td><td>{formatCount(bucket.requests)}</td></tr>
          {/each}
        </tbody>
      </table>
    </details>
  {/if}
</section>

<style>
  h3 {
    margin: 0 0 0.75rem;
    font-size: 1rem;
    font-weight: 500;
  }
  .frame {
    width: 100%;
  }
  svg {
    display: block;
    max-width: 100%;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .axis {
    fill: var(--muted);
    font-size: 11px;
  }
  .bar {
    fill: var(--accent);
  }
  .bar:hover {
    opacity: 0.8;
  }
  .empty {
    margin: 2rem 0;
    color: var(--muted);
    text-align: center;
  }
  details {
    margin-top: 0.5rem;
    color: var(--muted);
    font-size: 0.85rem;
  }
  summary {
    cursor: pointer;
  }
  table {
    margin-top: 0.5rem;
    border-collapse: collapse;
  }
  caption {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  th,
  td {
    padding: 0.2rem 1rem 0.2rem 0;
    text-align: left;
    font-variant-numeric: tabular-nums;
  }
</style>
