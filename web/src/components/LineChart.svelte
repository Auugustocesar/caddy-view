<script>
  import { niceMax } from '../lib/chart.js'
  import { formatClock, formatCount, formatDay, formatStamp } from '../lib/format.js'

  /**
   * Requests over time as one line, with the same period before it as a fainter one. The two share an axis and are laid
   * side by side by position, the first bucket of each against the first of the other.
   * @type {{title: string, buckets: Array<{start: string, requests: number}>, previous?: Array<{start: string, requests: number}>|null, labels?: 'time'|'date'}}
   */
  let { title, buckets, previous = null, labels = 'time' } = $props()

  const W = 864
  const H = 270
  const TOP = 12
  const BOTTOM = 244
  const AXIS_LABELS = 6 // the most labels under the line

  const hasPrevious = $derived(Boolean(previous) && previous.length > 0)
  const total = $derived(buckets.reduce((sum, b) => sum + b.requests, 0))
  const peak = $derived(buckets.reduce((best, b) => (b.requests > (best?.requests ?? -1) ? b : best), null))
  const top = $derived(niceMax(Math.max(0, ...buckets.map((b) => b.requests), ...(previous ?? []).map((b) => b.requests))))
  const stamp = $derived(labels === 'date' ? formatStamp : formatClock)

  const yOf = (value) => BOTTOM - (value / top) * (BOTTOM - TOP)
  const xOf = (i, n) => (n <= 1 ? 0 : (i / (n - 1)) * W)
  const r = (n) => Math.round(n * 10) / 10

  /** The line through the buckets, with the tangents flat at each point, as a Bezier path. */
  function linePath(series) {
    const n = series.length
    if (n === 0) return ''
    if (n === 1) return `M 0 ${r(yOf(series[0].requests))} L ${W} ${r(yOf(series[0].requests))}`
    let d = `M ${r(xOf(0, n))} ${r(yOf(series[0].requests))}`
    for (let i = 1; i < n; i++) {
      const x0 = xOf(i - 1, n)
      const x1 = xOf(i, n)
      const y0 = yOf(series[i - 1].requests)
      const y1 = yOf(series[i].requests)
      const mid = (x0 + x1) / 2
      d += ` C ${r(mid)} ${r(y0)} ${r(mid)} ${r(y1)} ${r(x1)} ${r(y1)}`
    }
    return d
  }

  const currentPath = $derived(linePath(buckets))
  const previousPath = $derived(hasPrevious ? linePath(previous) : '')
  const areaPath = $derived(currentPath ? `${currentPath} L ${W} ${BOTTOM} L 0 ${BOTTOM} Z` : '')

  // Labels under the line: the time of some of the buckets, evenly spaced.
  const ticks = $derived.by(() => {
    const n = buckets.length
    if (n === 0) return []
    const count = Math.min(AXIS_LABELS, n)
    const picked = new Set()
    for (let k = 0; k < count; k++) picked.add(count === 1 ? 0 : Math.round((k / (count - 1)) * (n - 1)))
    return [...picked].map((i) => ({ i, text: labels === 'date' ? formatDay(buckets[i].start) : formatClock(buckets[i].start) }))
  })
</script>

<section class="chart">
  <div class="head">
    <div>
      <h3>{title}</h3>
    </div>
    <div class="legend">
      <span><i class="dot now"></i>This period</span>
      {#if hasPrevious}<span><i class="dot before"></i>Previous period</span>{/if}
    </div>
  </div>

  {#if total === 0}
    <p class="empty">No traffic in this period</p>
  {:else}
    <div class="plot">
      <svg
        viewBox="0 0 {W} {H}"
        preserveAspectRatio="none"
        width="100%"
        height={H}
        role="img"
        aria-label="{title}, {formatCount(total)} requests in total, peaking at {formatCount(peak.requests)} around {stamp(peak.start)}"
      >
        <defs>
          <linearGradient id="area" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stop-color="var(--accent)" stop-opacity="0.28" />
            <stop offset="1" stop-color="var(--accent)" stop-opacity="0" />
          </linearGradient>
        </defs>
        {#each [0, 1 / 3, 2 / 3, 1] as f}
          <line class="grid" x1="0" x2={W} y1={TOP + f * (BOTTOM - TOP)} y2={TOP + f * (BOTTOM - TOP)} vector-effect="non-scaling-stroke" />
        {/each}
        <path data-area d={areaPath} fill="url(#area)" />
        {#if hasPrevious}
          <path data-series="previous" d={previousPath} class="previous" vector-effect="non-scaling-stroke" />
        {/if}
        <path data-series="current" d={currentPath} class="current" vector-effect="non-scaling-stroke" />
      </svg>
    </div>
    <div class="axis" aria-hidden="true">
      {#each ticks as tick (tick.i)}<span>{tick.text}</span>{/each}
    </div>
    <details>
      <summary>As table</summary>
      <table>
        <caption>{title}</caption>
        <thead><tr><th scope="col">Time</th><th scope="col">Requests</th></tr></thead>
        <tbody>
          {#each buckets as bucket}
            <tr><td>{stamp(bucket.start)}</td><td>{formatCount(bucket.requests)}</td></tr>
          {/each}
        </tbody>
      </table>
    </details>
  {/if}
</section>

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
  }
  h3 {
    margin: 0;
    font: 600 1.3rem / 1.2 var(--display);
    letter-spacing: -0.02em;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 1.25rem;
    color: var(--muted);
    font-size: 0.85rem;
  }
  .dot {
    display: inline-block;
    width: 0.5rem;
    height: 0.5rem;
    margin-right: 0.5rem;
    border-radius: 50%;
    background: var(--accent);
  }
  .dot.before {
    background: var(--faint);
  }
  .plot {
    position: relative;
  }
  svg {
    display: block;
    width: 100%;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .current {
    fill: none;
    stroke: var(--accent);
    stroke-width: 2.5;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .previous {
    fill: none;
    stroke: var(--faint);
    stroke-width: 1.5;
    stroke-dasharray: 5 5;
    stroke-linecap: round;
  }
  .axis {
    display: flex;
    justify-content: space-between;
    margin-top: 0.4rem;
    color: var(--muted);
    font: 400 0.75rem / 1.3 var(--mono);
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
