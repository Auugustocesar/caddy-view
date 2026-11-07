<script>
  import { formatBytes } from '../lib/format.js'
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
  const CLASSES = ['2xx', '3xx', '4xx', '5xx']

  /** @type {Set<string>} the classes turned off */
  let off = $state(new Set())
  let host = $state('')
  /** @type {number|null} */
  let selectedId = $state(null)

  const isAccessLine = (line) => line.status !== null || line.method !== null
  const hosts = $derived([...new Set(lines.map((l) => l.host).filter(Boolean))].sort())
  const visible = $derived(
    filterLines(lines, filter).filter((l) => {
      if (!isAccessLine(l)) return true // a line that is not a request is shown whatever the pills say
      if (off.has(statusClass(l.status))) return false
      return host === '' || l.host === host
    }),
  )
  const selected = $derived(lines.find((l) => l.id === selectedId) ?? null)

  function toggle(name) {
    const next = new Set(off)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    off = next
  }

  function choose(event, line) {
    if (event.type === 'keydown' && event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    selectedId = line.id
  }

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
  <span class="dot" aria-hidden="true"></span>
  <strong>{labels[status]}</strong>
  {#if reason}<span class="detail">{reason}</span>{/if}
</p>

<div class="toolbar">
  <input type="search" aria-label="Filter" placeholder="Filter lines: a path, a host, a status" value={filter} oninput={(e) => onfilter(e.currentTarget.value)} />
  {#each CLASSES as name}
    <button type="button" class="pill" data-class={name} aria-pressed={!off.has(name)} onclick={() => toggle(name)}><span class="swatch" aria-hidden="true"></span>{name}</button>
  {/each}
  <select aria-label="Host" bind:value={host}>
    <option value="">All hosts</option>
    {#each hosts as h}<option value={h}>{h}</option>{/each}
  </select>
  {#if paused}
    <button type="button" onclick={onresume}>Resume ({heldCount} new)</button>
  {:else}
    <button type="button" onclick={onpause}>Pause</button>
  {/if}
  <button type="button" class="ghost" onclick={onclear}>Clear</button>
</div>

<div class="main">
  <section class="panel" aria-label="Requests">
    <ol class="lines" role="listbox" aria-label="Requests" bind:this={list} {onscroll}>
      {#each visible as line (line.id)}
        <li
          role="option"
          tabindex="0"
          aria-selected={line.id === selectedId}
          data-testid="log-{line.id}"
          data-status={statusClass(line.status)}
          onclick={(e) => choose(e, line)}
          onkeydown={(e) => choose(e, line)}
        >
          {#if isAccessLine(line)}
            <time>{line.time ? line.time.toLocaleTimeString() : ''}</time>
            <span class="method">{line.method ?? ''}</span>
            <span class="host">{line.host ?? ''}</span>
            <span class="uri">{line.uri ?? ''}</span>
            <span class="code">{line.status ?? ''}</span>
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
  </section>

  <aside class="details" aria-label="Request">
    <h2>Request</h2>
    {#if !selected}
      <p class="empty">Choose a request to see what is known of it.</p>
    {:else if isAccessLine(selected)}
      <div class="title">{selected.method ?? ''} {selected.uri ?? ''}</div>
      <dl>
        <div><dt>Status</dt><dd class="code" data-status={statusClass(selected.status)}>{selected.status ?? ''}</dd></div>
        <div><dt>Time</dt><dd>{selected.time ? selected.time.toLocaleString() : ''}</dd></div>
        <div><dt>Host</dt><dd>{selected.host ?? ''}</dd></div>
        <div><dt>Client</dt><dd>{selected.client ?? ''}</dd></div>
        <div><dt>Duration</dt><dd>{formatDuration(selected.duration)}</dd></div>
        <div><dt>Response size</dt><dd>{selected.size === null ? '' : formatBytes(selected.size)}</dd></div>
      </dl>
      {#if selected.host}
        <button type="button" class="ghost" onclick={() => (host = selected.host)}>Only this host</button>
      {/if}
    {:else}
      <p class="raw-detail">{selected.raw}</p>
    {/if}
  </aside>
</div>

<style>
  .status {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    margin: 0 0 1rem;
    padding: 0.25rem 0.8rem;
    border: 1px solid var(--line2);
    border-radius: 999px;
    font-size: 0.85rem;
  }
  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--faint);
  }
  .status[data-state='live'] .dot {
    background: var(--ok);
  }
  .status[data-state='unavailable'] .dot,
  .status[data-state='closed'] .dot {
    background: var(--bad);
  }
  .status[data-state='reconnecting'] .dot,
  .status[data-state='connecting'] .dot {
    background: var(--warn);
  }
  .detail,
  .empty {
    color: var(--muted);
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    padding: 0.9rem 0;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    margin-bottom: 1.5rem;
  }
  .toolbar input {
    flex: 1 1 16rem;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.25rem 0.8rem;
    border-radius: 999px;
    color: var(--muted);
    font-size: 0.85rem;
  }
  .pill[aria-pressed='true'] {
    border-color: var(--text);
    color: var(--text);
  }
  .pill .swatch {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--ok);
  }
  .pill[data-class='3xx'] .swatch {
    background: var(--info);
  }
  .pill[data-class='4xx'] .swatch {
    background: var(--warn);
  }
  .pill[data-class='5xx'] .swatch {
    background: var(--bad);
  }
  .ghost {
    border-color: transparent;
    border-bottom: 2px solid var(--accent);
    border-radius: 0;
    padding: 0.2rem 0.1rem;
    background: transparent;
  }
  .main {
    display: flex;
    flex-wrap: wrap;
    gap: 2.5rem;
    align-items: flex-start;
  }
  .panel {
    flex: 2 1 38rem;
    min-width: 0;
    padding: 0.75rem 0.5rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius-lg);
    background: linear-gradient(155deg, var(--accent-soft), transparent 55%), var(--surface);
  }
  .lines {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 65vh;
    overflow: auto;
    font-family: var(--mono);
    font-size: 0.8rem;
  }
  .lines:empty {
    display: none;
  }
  li {
    display: grid;
    grid-template-columns: 5.5rem 3.5rem 11rem minmax(0, 1fr) 3.5rem 5rem;
    gap: 0 1rem;
    align-items: center;
    padding: 0.5rem 0.9rem;
    border-radius: var(--radius);
    cursor: pointer;
  }
  li:hover {
    background: var(--accent-soft);
  }
  li[aria-selected='true'] {
    background: var(--bad-soft);
    box-shadow: inset 3px 0 0 var(--accent);
  }
  li > span,
  li > time {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  li .raw {
    grid-column: 1 / -1;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  .code {
    justify-self: start;
    padding: 0 0.5rem;
    border-radius: 999px;
    font-weight: 500;
  }
  li[data-status='2xx'] .code,
  .code[data-status='2xx'] {
    background: var(--ok-soft);
    color: var(--ok);
  }
  li[data-status='3xx'] .code,
  .code[data-status='3xx'] {
    background: var(--info-soft);
    color: var(--info);
  }
  li[data-status='4xx'] .code,
  .code[data-status='4xx'] {
    background: var(--warn-soft);
    color: var(--warn);
  }
  li[data-status='5xx'] .code,
  .code[data-status='5xx'] {
    background: var(--bad-soft);
    color: var(--bad);
  }
  .method,
  time,
  .duration {
    color: var(--muted);
  }
  .duration {
    text-align: right;
  }
  .empty {
    padding: 0.75rem 0.9rem 0.25rem;
  }

  .details {
    flex: 1 1 18rem;
    min-width: 0;
  }
  .details h2 {
    margin: 0;
    padding-top: 0.9rem;
    border-top: 1px solid var(--line2);
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .title {
    padding: 1rem 0 0.4rem;
    overflow-wrap: anywhere;
    font: 600 1.25rem / 1.25 var(--display);
    letter-spacing: -0.015em;
  }
  dl {
    margin: 0 0 1rem;
  }
  dl div {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.55rem 0;
    border-bottom: 1px solid var(--border);
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    font-family: var(--mono);
    text-align: right;
    overflow-wrap: anywhere;
  }
  .raw-detail {
    font-family: var(--mono);
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }

  @media (max-width: 40rem) {
    li {
      grid-template-columns: 4.5rem 3rem minmax(0, 1fr) 3.5rem;
      grid-template-areas: 'time method code duration' 'uri uri uri uri';
      row-gap: 0.15rem;
    }
    li > time {
      grid-area: time;
    }
    li > .method {
      grid-area: method;
    }
    li > .host {
      display: none;
    }
    li > .uri {
      grid-area: uri;
    }
    li > .code {
      grid-area: code;
    }
    li > .duration {
      grid-area: duration;
    }
  }
</style>
