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
  // What is written under the marked line: the refusal of an apply, or Caddy's answer to the check.
  const markedMessage = $derived(markedLine === 0 ? '' : failure?.line ? failure.message : tidyCaddyError(check.error))
  // The lines that differ from the file, counting a replaced line once.
  const changedLines = $derived.by(() => {
    if (!dirty) return 0
    let added = 0
    let removed = 0
    for (const entry of diffLines(saved.content, draft)) {
      if (entry.type === 'add') added++
      else if (entry.type === 'del') removed++
    }
    return Math.max(added, removed)
  })
  const changes = $derived(showChanges ? withContext(diffLines(saved.content, draft), CONTEXT) : [])
  // The Caddyfile is not versioned in the sites-only mode, so the newest applied revision is the state in force.
  const loadedRevision = $derived(revisions.find((r) => r.status === 'applied' && (sitesOnly || r.sha256 === saved.sha256)))
  const currentRevision = $derived(loadedRevision?.number)

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

<section class="pagehead" aria-label="Caddyfile">
  <div>
    <div class="eyebrow">Edited here, checked by Caddy, then loaded</div>
    <h1>Caddyfile</h1>
  </div>
  {#if phase === 'ready' && !sitesOnly}
    <div class="actions">
      <button type="button" class="ghost" onclick={discard} disabled={!dirty || busy}>Revert</button>
      <button type="button" class="primary" onclick={apply} disabled={!canApply} aria-busy={busy}>Apply</button>
    </div>
  {/if}
</section>

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
  <section class="state" aria-label="State">
    <div class="it">
      <span class="eyebrow">Editor</span>
      <b>{dirty ? `${changedLines} ${changedLines === 1 ? 'line' : 'lines'} changed` : 'Same as loaded'}</b>
    </div>
    {#if !sitesOnly}
      <div class="it">
        <span class="eyebrow">Check</span>
        <b role="status" data-state={dirty ? check.state : 'idle'} title={statusText}>{statusText}</b>
      </div>
    {/if}
    <div class="it">
      <span class="eyebrow">Loaded in Caddy</span>
      <b>{#if loadedRevision}Revision {loadedRevision.number}, {when(loadedRevision.time)}{:else}No revision yet{/if}</b>
    </div>
    {#if !sitesOnly}
      <span class="spacer"></span>
      <span class="hint">Apply is off while the check has an error.</span>
    {/if}
  </section>

  {#if sitesOnly}
    <p class="note">
      In the sites-only backup mode caddy-view never writes the Caddyfile and keeps no copy of it, so it is shown here to be
      read. Edit it by hand; the revisions are of the sites you add in the Sites tab.
    </p>
  {/if}

  <div class="main">
    <div class="column">
      <div class="edwrap">
        <div class="tools" role="toolbar" aria-label="Editor tools">
          <span class="file" title={path}>{path || 'Caddyfile'}</span>
          <span class="spacer"></span>
          {#if !sitesOnly}
            <button type="button" class="ghost small" onclick={format} disabled={busy}>Format</button>
            <button type="button" class="ghost small" onclick={() => (showChanges = !showChanges)}>{showChanges ? 'Hide changes' : 'Show changes'}</button>
          {/if}
        </div>
        <div class="editor">
          <CodeEditor
            value={draft}
            label="Caddyfile editor"
            errorLine={markedLine}
            errorMessage={markedMessage}
            baseline={saved.content}
            readonly={sitesOnly}
            onchange={edit}
          />
        </div>
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
        <section class="diff changes" aria-label="Changes">
          {#if changes.length === 0}
            <p class="empty">No changes</p>
          {:else}
            {@render diffRows(changes)}
          {/if}
        </section>
      {/if}
    </div>

    <section class="history" aria-label="Revisions">
      <div class="head">
        <h2>Revisions</h2>
        <span class="detail">{sitesOnly ? 'A copy of the sites before every apply' : 'A copy before every apply'}</span>
      </div>
      {#if historyError}
        <p class="error">{historyError}</p>
      {/if}
      {#if historyLoaded && revisions.length === 0 && !historyError}
        <p class="empty">No revisions yet</p>
      {/if}
      {#if revisions.length > 0}
        <ul class="revisions">
          {#each revisions as rev (rev.number)}
            <li>
              <div class="rev">
                <div class="k">
                  <strong>Revision {rev.number}</strong>
                  {#if rev.number === currentRevision}<span class="badge" data-status="current">loaded</span>{/if}
                  <div class="detail">{label(rev.trigger)}, {when(rev.time)}</div>
                </div>
                <span class="badge" data-status={rev.status}>{label(rev.status)}</span>
                {#if !sitesOnly}
                  <button type="button" class="ghost small" aria-label="Compare revision {rev.number} with the file" onclick={() => compare(rev.number)}>
                    Compare
                  </button>
                {/if}
                {#if rev.status === 'applied' && rev.number !== currentRevision}
                  <button type="button" class="ghost small" aria-label="Roll back to revision {rev.number}" disabled={busy} onclick={() => (confirming = rev.number)}>
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
          <button type="button" class="ghost small" onclick={loadMore}>Load more</button>
        {/if}
      {/if}
    </section>
  </div>
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
  .pagehead {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1.5rem;
  }
  h1 {
    margin: 0.35rem 0 0;
    font: 600 clamp(2.2rem, 4.5vw, 3rem) / 1.05 var(--display);
    letter-spacing: -0.03em;
  }
  h2 {
    margin: 0;
    font: 600 1.3rem / 1.2 var(--display);
    letter-spacing: -0.02em;
  }
  .eyebrow {
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .actions {
    display: flex;
    gap: 0.75rem;
  }
  .ghost {
    border-color: transparent;
    border-bottom: 2px solid var(--accent);
    border-radius: 0;
    padding: 0.2rem 0.1rem;
    background: transparent;
  }
  .ghost:hover {
    border-color: transparent;
    border-bottom-color: var(--accent);
    color: var(--accent);
  }
  .ghost:disabled {
    border-bottom-color: var(--line2);
  }
  .small {
    font-size: 0.85rem;
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

  /* the state: its own strip, apart from the editor and the revisions */
  .state {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem 2rem;
    padding: 1rem 0;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    margin-bottom: 1.75rem;
  }
  .it {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
    max-width: 100%;
  }
  .it b {
    font-weight: 600;
  }
  /* What Caddy says can be a whole page of text: the strip keeps it to a line, and the title has the rest. */
  .it b[role='status'] {
    max-width: 38rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .it b[data-state='valid'] {
    color: var(--ok);
  }
  .it b[data-state='invalid'] {
    color: var(--bad);
  }
  .spacer {
    flex: 1;
  }
  .hint {
    color: var(--muted);
    font-size: 0.85rem;
  }

  .main {
    display: flex;
    flex-wrap: wrap;
    gap: 2.5rem;
    align-items: flex-start;
  }
  .column {
    flex: 2 1 38rem;
    min-width: 0;
  }
  .edwrap {
    overflow: hidden;
    border: 1px solid var(--line2);
    border-radius: var(--radius-lg);
    box-shadow: 0 0 0 3px var(--accent-soft);
  }
  .tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.9rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }
  .file {
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    padding: 0.2rem 0.65rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--bg);
    font: 500 0.8rem / 1.4 var(--mono);
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
    border-left: 3px solid var(--bad);
    background: var(--bad-soft);
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
  .detail {
    font-size: 0.85rem;
  }

  .history {
    flex: 1 1 20rem;
    min-width: 0;
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.5rem;
    padding-bottom: 0.6rem;
    border-bottom: 1px solid var(--border);
  }
  .revisions {
    margin: 0 0 0.75rem;
    padding: 0;
    list-style: none;
  }
  .revisions li {
    padding: 0.8rem 0;
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
  .rev .k {
    flex: 1 1 8rem;
    min-width: 0;
  }
  .badge {
    padding: 0.05rem 0.55rem;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font: 500 0.7rem / 1.5 var(--mono);
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .badge[data-status='applied'] {
    color: var(--ok);
  }
  .badge[data-status='failed'],
  .badge[data-status='recovery-required'] {
    color: var(--bad);
  }
  .badge[data-status='current'] {
    background: var(--ok-soft);
    color: var(--ok);
  }
  .confirm {
    margin-top: 0.5rem;
    padding: 0.6rem 0.8rem;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  .confirm p {
    margin: 0 0 0.5rem;
  }

  .diff {
    margin-top: 0.75rem;
    padding: 0.5rem 0;
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .revisions .diff {
    margin: 0.5rem 0 0;
    padding: 0.25rem 0;
    background: var(--surface-2);
    border: 0;
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
    background: var(--ok-soft);
  }
  .row[data-diff='del'] {
    background: var(--bad-soft);
  }
  .row[data-diff='skip'] {
    color: var(--muted);
  }
  .sign {
    flex: none;
    color: var(--muted);
  }
</style>
