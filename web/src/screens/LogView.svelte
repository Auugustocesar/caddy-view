<script>
  import { filterLines, formatDuration, statusClass } from '../lib/logline.js'

  /**
   * @type {{
   *   lines: Array<import('../lib/logstream.js').StreamLine>,
   *   status: import('../lib/logstream.js').StreamStatus,
   *   reason?: string,
   *   paused?: boolean,
   *   heldCount?: number,
   *   filter?: string,
   *   onpause: () => void,
   *   onresume: () => void,
   *   onclear: () => void,
   *   onfilter: (text: string) => void,
   * }}
   */
  let { lines, status, reason = '', paused = false, heldCount = 0, filter = '', onpause, onresume, onclear, onfilter } = $props()

  const labels = {
    connecting: 'Connecting…',
    live: 'Live',
    reconnecting: 'Reconnecting…',
    unavailable: 'Unavailable',
    closed: 'Disconnected',
  }

  const visible = $derived(filterLines(lines, filter))
  const isAccessLine = (line) => line.status !== null || line.method !== null

  // Follow the newest line, unless the reader has scrolled up to look at something.
  let list = $state()
  let following = true

  function onscroll() {
    following = list.scrollHeight - list.scrollTop - list.clientHeight < 24
  }

  $effect(() => {
    visible.length // re-run when lines arrive
    if (list && following) list.scrollTop = list.scrollHeight
  })
</script>

<p class="status" role="status" data-state={status}>
  <strong>{labels[status]}</strong>
  {#if reason}<span class="detail">{reason}</span>{/if}
</p>

<div class="toolbar">
  <input type="search" aria-label="Filter" placeholder="Filter lines" value={filter} oninput={(e) => onfilter(e.currentTarget.value)} />
  {#if paused}
    <button type="button" onclick={onresume}>Resume ({heldCount} new)</button>
  {:else}
    <button type="button" onclick={onpause}>Pause</button>
  {/if}
  <button type="button" onclick={onclear}>Clear</button>
</div>

<ol class="lines" bind:this={list} {onscroll}>
  {#each visible as line (line.id)}
    <li data-testid="log-{line.id}" data-status={statusClass(line.status)}>
      {#if isAccessLine(line)}
        {#if line.time}<time>{line.time.toLocaleTimeString()}</time>{/if}
        <span class="code">{line.status ?? ''}</span>
        <span class="method">{line.method ?? ''}</span>
        <span class="host">{line.host ?? ''}</span>
        <span class="uri">{line.uri ?? ''}</span>
        <span class="duration">{formatDuration(line.duration)}</span>
      {:else}
        <span class="raw">{line.raw}</span>
      {/if}
    </li>
  {/each}
</ol>

{#if lines.length === 0}
  {#if status === 'live'}
    <p class="empty">Waiting for requests…</p>
  {/if}
{:else if visible.length === 0}
  <p class="empty">No lines match the filter</p>
{/if}

<style>
  .status {
    margin: 0 0 0.75rem;
  }
  .status[data-state='live'] strong {
    color: var(--ok);
  }
  .status[data-state='unavailable'] strong,
  .status[data-state='closed'] strong {
    color: var(--bad);
  }
  .status[data-state='reconnecting'] strong,
  .status[data-state='connecting'] strong {
    color: var(--warn);
  }
  .detail,
  .empty {
    color: var(--muted);
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }
  .toolbar input {
    flex: 1 1 14rem;
  }
  .lines {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 65vh;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    font-family: var(--mono);
    font-size: 0.8rem;
  }
  .lines:empty {
    display: none;
  }
  li {
    display: flex;
    flex-wrap: wrap;
    gap: 0 0.75rem;
    padding: 0.2rem 0.75rem;
    border-bottom: 1px solid var(--border);
  }
  li[data-status='5xx'] .code {
    color: var(--bad);
  }
  li[data-status='4xx'] .code {
    color: var(--warn);
  }
  li[data-status='2xx'] .code {
    color: var(--ok);
  }
  .method,
  time,
  .duration {
    color: var(--muted);
  }
  .uri,
  .raw {
    overflow-wrap: anywhere;
  }
</style>
