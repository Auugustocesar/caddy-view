<script>
  import CodeEditor from '../components/CodeEditor.svelte'
  import {
    applyCaddyfile,
    formatCaddyfile,
    getCaddyfile,
    getRevision,
    getRevisions,
    getSystem,
    rollbackRevision,
    validateCaddyfile,
  } from '../lib/api.js'
  import { tidyCaddyError } from '../lib/caddy-error.js'
  import { diffLines, withContext } from '../lib/diff.js'

  /** @type {{validateDelay?: number}} */
  let { validateDelay = 500 } = $props()

  const PAGE = 20
  const CONTEXT = 2

  /** @type {'loading'|'off'|'ready'|'failed'} */
  let phase = $state('loading')
  let loadError = $state('')
  let path = $state('')
  // In the sites-only backup mode caddy-view never writes the Caddyfile: it shows it, and the history is of the sites.
  let sitesOnly = $state(false)

  let saved = $state({ content: '', sha256: '' })
  let draft = $state('')
  /** @type {{state: 'idle'|'checking'|'valid'|'invalid', error: string, line: number, warnings: Array<{message: string, line?: number}>}} */
  let check = $state({ state: 'idle', error: '', line: 0, warnings: [] })
  let notice = $state('')
  /** @type {{message: string, code?: string, line?: number} | null} */
  let failure = $state(null)
  let busy = $state(false)
  let showChanges = $state(false)

  let revisions = $state([])
  let next = $state(null)
  let historyLoaded = $state(false)
  let historyError = $state('')
  /** @type {{number: number, content: string} | null} */
  let comparing = $state(null)
  /** @type {number | null} */
  let confirming = $state(null)

  const dirty = $derived(draft !== saved.content)
  const canApply = $derived(dirty && check.state === 'valid' && !busy)
  const markedLine = $derived(failure?.line ? failure.line : check.state === 'invalid' ? check.line : 0)
  const changes = $derived(showChanges ? withContext(diffLines(saved.content, draft), CONTEXT) : [])
  // The Caddyfile is not versioned in the sites-only mode, so the newest applied revision is the state in force.
  const currentRevision = $derived(
    revisions.find((r) => r.status === 'applied' && (sitesOnly || r.sha256 === saved.sha256))?.number,
  )

  const statusText = $derived.by(() => {
    if (!dirty) return notice || 'No changes'
    switch (check.state) {
      case 'checking':
        return 'Checking…'
      case 'valid':
        return 'Valid — ready to apply'
      case 'invalid':
        return check.line > 0 ? `Line ${check.line}: ${tidyCaddyError(check.error)}` : tidyCaddyError(check.error)
      default:
        return 'Unsaved changes'
    }
  })

  const label = (text) => text.replaceAll('-', ' ')
  const when = (time) => new Date(time).toLocaleString()
  const problem = (err) => ({ message: tidyCaddyError(err.message), code: err.code, line: err.line })

  async function readFile() {
    const file = await getCaddyfile()
    saved = { content: file.content, sha256: file.sha256 }
    draft = file.content
  }

  async function readHistory() {
    try {
      const page = await getRevisions({ limit: PAGE })
      revisions = page.revisions
      next = page.next
      historyError = ''
    } catch (err) {
      historyError = err.message
    } finally {
      historyLoaded = true
    }
  }

  async function open() {
    try {
      const system = await getSystem()
      if (!system.editing?.enabled) {
        phase = 'off'
        return
      }
      path = system.editing.caddyfile ?? ''
      sitesOnly = system.editing.backupMode === 'sites-only'
      await readFile()
      phase = 'ready'
      readHistory()
    } catch (err) {
      loadError = err.message
      phase = 'failed'
    }
  }

  open()

  // Every edit is checked with Caddy after a pause. A newer edit makes an older check's answer worthless.
  let latest = 0
  $effect(() => {
    const text = draft
    const id = ++latest
    if (phase !== 'ready' || sitesOnly) return
    if (text === saved.content) {
      check = { state: 'idle', error: '', line: 0, warnings: [] }
      return
    }
    check = { state: 'checking', error: '', line: 0, warnings: [] }
    const timer = setTimeout(async () => {
      try {
        const answer = await validateCaddyfile(text)
        if (id !== latest) return
        check = answer.valid
          ? { state: 'valid', error: '', line: 0, warnings: answer.warnings ?? [] }
          : { state: 'invalid', error: answer.error ?? 'Caddy does not accept this', line: answer.line ?? 0, warnings: [] }
      } catch (err) {
        if (id === latest) check = { state: 'invalid', error: err.message, line: 0, warnings: [] }
      }
    }, validateDelay)
    return () => clearTimeout(timer)
  })

  function edit(text) {
    draft = text
    notice = ''
    failure = null
  }

  function discard() {
    draft = saved.content
    notice = ''
    failure = null
  }

  async function format() {
    busy = true
    failure = null
    try {
      draft = (await formatCaddyfile(draft)).content
    } catch (err) {
      failure = problem(err)
    } finally {
      busy = false
    }
  }

  async function apply() {
    if (!canApply) return
    busy = true
    failure = null
    try {
      const { revision } = await applyCaddyfile(draft, saved.sha256)
      await readFile()
      notice = `Applied as revision ${revision.number}`
      await readHistory()
    } catch (err) {
      failure = problem(err)
    } finally {
      busy = false
    }
  }

  async function reload() {
    failure = null
    try {
      await readFile()
      notice = ''
    } catch (err) {
      failure = problem(err)
    }
  }

  async function loadMore() {
    try {
      const page = await getRevisions({ limit: PAGE, before: next })
      revisions = [...revisions, ...page.revisions]
      next = page.next
    } catch (err) {
      historyError = err.message
    }
  }

  async function compare(number) {
    if (comparing?.number === number) {
      comparing = null
      return
    }
    try {
      const revision = await getRevision(number)
      comparing = { number, content: revision.content ?? '' }
    } catch (err) {
      historyError = err.message
    }
  }

  async function rollBack(number) {
    busy = true
    failure = null
    try {
      const { revision } = await rollbackRevision(number)
      confirming = null
      await readFile()
      notice = `Rolled back to revision ${number}, as revision ${revision.number}`
      await readHistory()
    } catch (err) {
      failure = problem(err)
      confirming = null
    } finally {
      busy = false
    }
  }
</script>

<header class="page">
  <h1>Caddyfile</h1>
  <p class="subtitle">{path || 'Edit, check and apply the Caddyfile Caddy runs.'}</p>
</header>

{#if phase === 'failed'}
  <p class="error" role="alert">{loadError}</p>
{:else if phase === 'off'}
  <p class="note">
    Editing is off. Start caddy-view with <code>--allow-edit</code>, <code>--caddyfile</code> and the history database to
    edit the Caddyfile here.
  </p>
{:else if phase === 'loading'}
  <p class="empty">Loading…</p>
{:else}
  {#if sitesOnly}
    <p class="note">
      In the sites-only backup mode caddy-view never writes the Caddyfile and keeps no copy of it, so it is shown here to be
      read. Edit it by hand; the history below is of the sites you add in the Sites tab.
    </p>
  {:else}
    <div class="toolbar">
      <button type="button" onclick={format} disabled={busy}>Format</button>
      <button type="button" onclick={() => (showChanges = !showChanges)}>{showChanges ? 'Hide changes' : 'Show changes'}</button>
      <button type="button" onclick={discard} disabled={!dirty || busy}>Discard changes</button>
      <span class="spacer"></span>
      <span class="status" role="status" data-state={dirty ? check.state : 'idle'}>{statusText}</span>
      <button type="button" class="primary" onclick={apply} disabled={!canApply} aria-busy={busy}>Apply</button>
    </div>
  {/if}

  <div class="card editor">
    <CodeEditor value={draft} label="Caddyfile editor" errorLine={markedLine} readonly={sitesOnly} onchange={edit} />
  </div>

  {#if dirty && check.warnings.length > 0}
    <ul class="warnings">
      {#each check.warnings as warning}
        <li>Warning: {warning.message}{warning.line ? ` (line ${warning.line})` : ''}</li>
      {/each}
    </ul>
  {/if}

  {#if failure}
    <div class="failure" role="alert">
      <p>{failure.message}</p>
      {#if failure.code === 'conflict'}
        <button type="button" onclick={reload}>Reload from disk</button>
      {/if}
    </div>
  {/if}

  {#if showChanges}
    <section class="card diff" aria-label="Changes">
      {#if changes.length === 0}
        <p class="empty">No changes</p>
      {:else}
        {@render diffRows(changes)}
      {/if}
    </section>
  {/if}

  <section class="history" aria-label="History">
    <h2>History</h2>
    {#if historyError}
      <p class="error">{historyError}</p>
    {/if}
    {#if historyLoaded && revisions.length === 0 && !historyError}
      <p class="empty">No revisions yet</p>
    {/if}
    {#if revisions.length > 0}
      <ul class="card revisions">
        {#each revisions as rev (rev.number)}
          <li>
            <div class="rev">
              <strong>Revision {rev.number}</strong>
              <span class="detail">{label(rev.trigger)}</span>
              <span class="badge" data-status={rev.status}>{label(rev.status)}</span>
              {#if rev.number === currentRevision}<span class="badge" data-status="current">current</span>{/if}
              <span class="detail">{when(rev.time)}</span>
              <span class="spacer"></span>
              {#if !sitesOnly}
                <button type="button" aria-label="Compare revision {rev.number} with the file" onclick={() => compare(rev.number)}>
                  Compare
                </button>
              {/if}
              {#if rev.status === 'applied' && rev.number !== currentRevision}
                <button type="button" aria-label="Roll back to revision {rev.number}" disabled={busy} onclick={() => (confirming = rev.number)}>
                  Roll back
                </button>
              {/if}
            </div>
            {#if rev.error}<p class="error">{rev.error}</p>{/if}
            {#if confirming === rev.number}
              <div class="confirm">
                <p>Roll back to revision {rev.number}?</p>
                {#if sitesOnly}<p class="note">This restores the managed sites of that revision. The Caddyfile is left as it is.</p>{/if}
                {#if dirty}<p class="note">Your unsaved changes will be lost.</p>{/if}
                <button type="button" class="primary" disabled={busy} onclick={() => rollBack(rev.number)}>Confirm roll back</button>
                <button type="button" onclick={() => (confirming = null)}>Cancel</button>
              </div>
            {/if}
            {#if comparing?.number === rev.number}
              {@const rows = withContext(diffLines(comparing.content, saved.content), CONTEXT)}
              <section class="diff" aria-label="Revision {rev.number} against the file">
                {#if rows.length === 0}
                  <p class="empty">This revision is the same as the file</p>
                {:else}
                  {@render diffRows(rows)}
                {/if}
              </section>
            {/if}
          </li>
        {/each}
      </ul>
      {#if next !== null}
        <button type="button" onclick={loadMore}>Load more</button>
      {/if}
    {/if}
  </section>
{/if}

{#snippet diffRows(rows)}
  <div class="rows">
    {#each rows as row}
      {#if row.type === 'skip'}
        <div class="row" data-diff="skip">⋯ {row.count} unchanged {row.count === 1 ? 'line' : 'lines'}</div>
      {:else}
        <div class="row" data-diff={row.type}><span class="sign" aria-hidden="true">{row.type === 'add' ? '+' : row.type === 'del' ? '−' : ' '}</span><code>{row.text}</code></div>
      {/if}
    {/each}
  </div>
{/snippet}

<style>
  .page {
    margin-bottom: 1.25rem;
  }
  h1 {
    margin: 0;
    font-size: 2.1rem;
    font-weight: 300;
    letter-spacing: -0.02em;
  }
  h2 {
    margin: 2rem 0 0.75rem;
    font-size: 1.05rem;
    font-weight: 500;
  }
  .subtitle {
    margin: 0.25rem 0 0;
    color: var(--muted);
    font-family: var(--mono);
    font-size: 0.85rem;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.75rem;
  }
  .spacer {
    flex: 1;
  }
  .status {
    color: var(--muted);
    font-size: 0.9rem;
  }
  .status[data-state='valid'] {
    color: var(--ok);
  }
  .status[data-state='invalid'] {
    color: var(--bad);
  }
  .primary {
    border-color: var(--accent);
    background: var(--accent);
    color: var(--accent-text);
    font-weight: 600;
  }
  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }
  .card {
    border-radius: var(--radius-lg);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .editor {
    height: clamp(18rem, 55vh, 38rem);
    overflow: hidden;
  }
  .warnings {
    margin: 0.75rem 0 0;
    padding-left: 1.25rem;
    color: var(--warn);
  }
  .failure {
    margin-top: 0.75rem;
    padding: 0.75rem 1rem;
    border-radius: var(--radius);
    border-left: 4px solid var(--bad);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .failure p {
    margin: 0 0 0.25rem;
    color: var(--bad);
  }
  .error {
    color: var(--bad);
  }
  .note,
  .empty,
  .detail {
    color: var(--muted);
  }
  .note {
    color: var(--warn);
  }
  .diff {
    margin-top: 0.75rem;
    padding: 0.5rem 0;
    overflow-x: auto;
  }
  .revisions .diff {
    margin: 0.5rem 0 0;
    padding: 0.25rem 0;
    background: var(--surface-2);
    border-radius: var(--radius);
    box-shadow: none;
  }
  .rows {
    font-family: var(--mono);
    font-size: 0.85rem;
  }
  .row {
    display: flex;
    gap: 0.75rem;
    padding: 0 1rem;
    white-space: pre;
  }
  .row[data-diff='add'] {
    background: color-mix(in srgb, var(--ok) 16%, transparent);
  }
  .row[data-diff='del'] {
    background: color-mix(in srgb, var(--bad) 16%, transparent);
  }
  .row[data-diff='skip'] {
    color: var(--muted);
  }
  .sign {
    flex: none;
    color: var(--muted);
  }
  .revisions {
    margin: 0 0 0.75rem;
    padding: 0.25rem 1rem;
    list-style: none;
  }
  .revisions li {
    padding: 0.65rem 0;
    border-bottom: 1px solid var(--border);
  }
  .revisions li:last-child {
    border-bottom: 0;
  }
  .rev {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem 0.75rem;
  }
  .badge {
    padding: 0.05rem 0.55rem;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 0.75rem;
  }
  .badge[data-status='applied'] {
    color: var(--ok);
  }
  .badge[data-status='failed'],
  .badge[data-status='recovery-required'] {
    color: var(--bad);
  }
  .badge[data-status='current'] {
    background: var(--accent);
    color: var(--accent-text);
  }
  .confirm {
    margin-top: 0.5rem;
    padding: 0.5rem 0.75rem;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  .confirm p {
    margin: 0 0 0.5rem;
  }
</style>
