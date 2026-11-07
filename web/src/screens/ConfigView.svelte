<script>
  import { getSystem } from '../lib/api.js'
  import { formatBytes, formatCount, formatDuration, formatStamp } from '../lib/format.js'

  /** @type {{config?: object | null, loadError?: string}} */
  let { config = null, loadError = '' } = $props()

  const text = $derived(config === null ? '' : JSON.stringify(config, null, 2))
  let feedback = $state('')

  /** @type {any} what /api/system answered, or null before it did */
  let system = $state(null)
  let statusError = $state(false)
  let refreshing = $state(false)

  async function refresh() {
    refreshing = true
    try {
      system = await getSystem()
      statusError = false
    } catch {
      statusError = true
    } finally {
      refreshing = false
    }
  }

  // Read once when the tab opens; the Refresh button reads it again. It does not poll.
  refresh()

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      feedback = 'Copied'
    } catch {
      feedback = 'Copy failed'
    }
  }

  // The message disappears on its own.
  $effect(() => {
    if (!feedback) return
    const timer = setTimeout(() => (feedback = ''), 2000)
    return () => clearTimeout(timer)
  })

  const MODES = {
    full: 'Full: every revision keeps the Caddyfile',
    'sites-only': 'Sites only: the Caddyfile is never written or copied',
  }
  const when = (time) => (time ? formatStamp(time) : 'none yet')
  const limit = (bytes) => (bytes ? formatBytes(bytes) : 'no limit')

  const editing = $derived(system?.editing ?? null)
  const drift = $derived(editing?.drift ?? null)
</script>

<header class="page">
  <div class="eyebrow">How caddy-view is set up, and what Caddy runs</div>
  <h1>Config</h1>
</header>

<div class="toolbar">
  <button type="button" onclick={refresh} disabled={refreshing}>Refresh</button>
</div>

{#if statusError}
  <p class="note">The status could not be read. The configuration below is unaffected.</p>
{/if}

{#if system}
  <div class="cards">
    {#if system.caddy}
    <section class="card" aria-label="Caddy">
      <h2>Caddy</h2>
      <dl>
        <dt>State</dt>
        <dd data-state={system.caddy.reachable ? 'ok' : 'bad'}>{system.caddy.reachable ? 'Reachable' : 'Not reachable'}</dd>
        <dt>Admin API</dt>
        <dd><code>{system.caddy.endpoint}</code></dd>
        {#if system.caddy.error}
          <dt>Error</dt>
          <dd>{system.caddy.error}</dd>
        {/if}
      </dl>
    </section>
    {/if}

    <section class="card" aria-label="Editing">
      <h2>Editing</h2>
      {#if !editing?.enabled}
        <p>Off. Start caddy-view with <code>--allow-edit</code>, <code>--caddyfile</code> and the history database to edit here.</p>
      {:else}
        <dl>
          <dt>Caddyfile</dt>
          <dd><code>{editing.caddyfile}</code></dd>
          <dt>Backup mode</dt>
          <dd>{MODES[editing.backupMode] ?? editing.backupMode}</dd>
          {#if editing.managedFile}
            <dt>Managed sites</dt>
            <dd>
              <code>{editing.managedFile}</code>
              <span class="detail">{editing.imported ? 'imported by the Caddyfile' : 'not imported by the Caddyfile'}</span>
            </dd>
          {/if}
          {#if drift?.known}
            <dt>Caddyfile</dt>
            <dd data-state={drift.drifted ? 'bad' : 'ok'}>
              {drift.drifted ? `Edited outside caddy-view since revision ${drift.revision}` : `The newest revision, ${drift.revision}`}
            </dd>
          {/if}
          {#if drift}
            <dt>Running</dt>
            <dd data-state={drift.running === 'differs' ? 'bad' : drift.running === 'matches' ? 'ok' : ''}>
              {drift.running === 'matches' ? 'Caddy runs it' : drift.running === 'differs' ? 'Caddy does not run it' : 'Not known'}
            </dd>
          {/if}
          {#if editing.error}
            <dt>Error</dt>
            <dd>{editing.error}</dd>
          {/if}
        </dl>
      {/if}
    </section>

    {#if system.ingestion}
      <section class="card" aria-label="Access log">
        <h2>Access log</h2>
        <dl>
          <dt>State</dt>
          <dd data-state={system.ingestion.state === 'failing' ? 'bad' : system.ingestion.state === 'following' ? 'ok' : ''}>{system.ingestion.state}</dd>
          <dt>File</dt>
          <dd>{#if system.ingestion.file}<code>{system.ingestion.file}</code>{:else}none found yet{/if}</dd>
          <dt>Newest entry</dt>
          <dd>{when(system.ingestion.newestEntry)}</dd>
          <dt>Gaps</dt>
          <dd>{formatCount(system.ingestion.gaps)}</dd>
          <dt>Skipped lines</dt>
          <dd>{formatCount(system.ingestion.skippedLines)}</dd>
          {#if system.ingestion.error}
            <dt>Error</dt>
            <dd>{system.ingestion.error}</dd>
          {/if}
        </dl>
      </section>
    {/if}

    {#if system.scraper}
      <section class="card" aria-label="Metrics scraper">
        <h2>Metrics scraper</h2>
        <dl>
          <dt>State</dt>
          <dd data-state={system.scraper.state === 'failing' ? 'bad' : system.scraper.state === 'ok' ? 'ok' : ''}>{system.scraper.state}</dd>
          <dt>Last success</dt>
          <dd>{when(system.scraper.lastSuccess)}</dd>
          {#if system.scraper.error}
            <dt>Error</dt>
            <dd>{system.scraper.error}</dd>
          {/if}
        </dl>
      </section>
    {/if}

    {#if system.database}
      <section class="card" aria-label="Database">
        <h2>Database</h2>
        {#if system.database.error}
          <p>{system.database.error}</p>
        {:else}
          <dl>
            <dt>Kind</dt>
            <dd>{system.database.kind}</dd>
            {#if system.database.file}
              <dt>File</dt>
              <dd><code>{system.database.file}</code></dd>
            {/if}
            <dt>Size</dt>
            <dd>{formatBytes(system.database.sizeBytes)}</dd>
            {#if system.database.schemaVersion}
              <dt>Schema</dt>
              <dd>version {system.database.schemaVersion}</dd>
            {/if}
          </dl>
        {/if}
      </section>
    {/if}

    {#if system.retention}
      <section class="card" aria-label="Retention">
        <h2>Retention</h2>
        <dl>
          <dt>Requests and samples</dt>
          <dd>{formatDuration(system.retention.rawSeconds)}</dd>
          <dt>Hour rollups</dt>
          <dd>{formatDuration(system.retention.rollupsSeconds)}</dd>
          <dt>Size cap</dt>
          <dd>{limit(system.retention.maxBytes)}</dd>
        </dl>
      </section>
    {/if}
  </div>
{/if}

<h2 class="running">Running configuration</h2>

{#if loadError}
  <p class="error" role="alert">{loadError}</p>
{/if}

{#if config !== null}
  <div class="toolbar">
    <button type="button" onclick={copy}>Copy</button>
    <span aria-live="polite">{feedback}</span>
  </div>
  <pre data-testid="config-json">{text}</pre>
{:else if !loadError}
  <p class="empty">Loading…</p>
{/if}

<style>
  .page {
    margin-bottom: 1.75rem;
  }
  .eyebrow {
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  h1 {
    margin: 0.35rem 0 0;
    font: 600 clamp(2.2rem, 4.5vw, 3rem) / 1.05 var(--display);
    letter-spacing: -0.03em;
  }
  h2 {
    margin: 0 0 0.75rem;
    font: 600 1.3rem / 1.2 var(--display);
    letter-spacing: -0.02em;
  }
  .running {
    margin-top: 3rem;
  }
  .toolbar {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 0.75rem;
    color: var(--muted);
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 20rem), 1fr));
    gap: 2rem 2.5rem;
  }
  /* A section of the page: a hairline over it and its label, with no box around it. */
  .card {
    min-width: 0;
    padding-top: 0.9rem;
    border-top: 1px solid var(--line2);
  }
  .card h2 {
    margin: 0 0 0.75rem;
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  dl {
    display: grid;
    grid-template-columns: minmax(0, 8rem) minmax(0, 1fr);
    gap: 0;
    margin: 0;
  }
  dt,
  dd {
    padding: 0.55rem 0;
    border-bottom: 1px solid var(--border);
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    padding-left: 0.75rem;
    overflow-wrap: anywhere;
  }
  dt:last-of-type,
  dd:last-of-type {
    border-bottom: 0;
  }
  dd[data-state='ok'] {
    color: var(--ok);
  }
  dd[data-state='bad'] {
    color: var(--bad);
  }
  .detail {
    display: block;
    color: var(--muted);
    font-size: 0.8rem;
  }
  .card p {
    margin: 0;
  }
  pre {
    margin: 0;
    padding: 1rem 1.25rem;
    overflow: auto;
    max-height: 75vh;
    border: 1px solid var(--line2);
    border-radius: var(--radius-lg);
    background: var(--surface);
    font: 400 0.85rem / 1.6 var(--mono);
  }
  .error {
    color: var(--bad);
  }
  .note {
    color: var(--warn);
  }
  .empty {
    color: var(--muted);
  }
</style>
