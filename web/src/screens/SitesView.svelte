<script>
  import SiteWizard from '../components/SiteWizard.svelte'
  import { addImport, deleteSite, getManagedSites, getSites, updateSite } from '../lib/api.js'
  import { tidyCaddyError } from '../lib/caddy-error.js'
  import { formFromSite, isProtected, payloadFromForm, siteSub, tlsLabel } from '../lib/site-form.js'

  /** @type {{previewDelay?: number}} */
  let { previewDelay = 400 } = $props()

  /** @type {'loading'|'off'|'ready'|'failed'} */
  let phase = $state('loading')
  let loadError = $state('')
  /** @type {{sites: Array<any>, managedFile: string, imported: boolean, accessLog: boolean, backupMode: string}} */
  let data = $state({ sites: [], managedFile: '', imported: true, accessLog: false, backupMode: 'full' })
  /** @type {{sites: Array<{server: string, host: string, upstreams: Array<{address: string}>}>} | null} */
  let running = $state(null)
  let runningError = $state('')

  /** The site the form is for: one that exists, or null for a new one. A new key starts the form over. */
  let editing = $state({ site: /** @type {object | null} */ (null), key: 0 })
  /** @type {string | null} */
  let confirming = $state(null)
  let notice = $state('')
  let failure = $state('')
  let busy = $state(false)

  const enabledHosts = $derived(new Set(data.sites.filter((s) => s.enabled).map((s) => s.hostname.toLowerCase())))
  const isManaged = (host) => enabledHosts.has(host.toLowerCase())
  const enabledCount = $derived(data.sites.filter((s) => s.enabled).length)
  const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`

  async function refresh() {
    try {
      data = await getManagedSites()
      phase = 'ready'
    } catch (err) {
      // Without --allow-edit the routes are not there at all: that is a setting, not a failure.
      if (err.status === 404) phase = 'off'
      else {
        loadError = err.message
        phase = 'failed'
        return
      }
    }
    try {
      running = await getSites()
      runningError = ''
    } catch (err) {
      running = null
      runningError = err.message
    }
  }

  refresh()

  /** Runs a change: the notice and the failure of the one before are gone, and the list is read again after it. */
  async function change(work) {
    if (busy) return
    busy = true
    notice = ''
    failure = ''
    try {
      notice = await work()
      await refresh()
    } catch (err) {
      failure = tidyCaddyError(err.message)
    } finally {
      busy = false
    }
  }

  const addTheImport = () =>
    change(async () => {
      const { revision } = await addImport()
      return `Added the import of ${data.managedFile} (revision ${revision.number})`
    })

  const toggle = (site) =>
    change(async () => {
      const { revision } = await updateSite(site.id, { ...payloadFromForm(formFromSite(site)), enabled: !site.enabled })
      return `${site.enabled ? 'Disabled' : 'Enabled'} ${site.hostname} (revision ${revision.number})`
    })

  const remove = (site) =>
    change(async () => {
      const { revision } = await deleteSite(site.id)
      confirming = null
      return `Deleted ${site.hostname} (revision ${revision.number})`
    })

  /** The form goes back to a blank one for a new site. */
  function startOver() {
    editing = { site: null, key: editing.key + 1 }
  }

  async function saved(result, existing) {
    startOver()
    notice = `${existing ? 'Saved' : 'Added'} ${result.site?.hostname ?? existing?.hostname ?? 'the site'} (revision ${result.revision.number})`
    failure = ''
    await refresh()
  }

  function edit(site) {
    editing = { site, key: editing.key + 1 }
    confirming = null
    notice = ''
    failure = ''
  }
</script>

<header class="pagehead">
  <div>
    <div class="eyebrow">Reverse proxies and redirects that caddy-view keeps for you</div>
    <h1>Sites</h1>
  </div>
  {#if phase === 'ready'}
    <button type="button" class="primary" onclick={startOver} disabled={!data.imported || busy}>New site</button>
  {/if}
</header>

{#if phase === 'failed'}
  <p class="error" role="alert">{loadError}</p>
{:else if phase === 'loading'}
  <p class="empty">Loading…</p>
{:else}
  {#if phase === 'off'}
    <p class="note">
      Managing sites needs editing to be on. Start caddy-view with <code>--allow-edit</code>, <code>--caddyfile</code> and the
      history database.
    </p>
  {:else}
    {#if !data.imported}
      <section class="setup" aria-label="Setup">
        <p>
          The Caddyfile does not import the file caddy-view writes its sites to, so a site added here would never be served.
        </p>
        {#if data.backupMode === 'sites-only'}
          <p>caddy-view does not write the Caddyfile in the sites-only backup mode. Add this line to it yourself:</p>
          <p><code>import {data.managedFile}</code></p>
        {:else}
          <p><code>{data.managedFile}</code></p>
          <button type="button" class="primary" onclick={addTheImport} disabled={busy}>Add the import</button>
        {/if}
      </section>
    {/if}

    <section class="file" aria-label="Managed file">
      <span class="tag" data-state={data.imported ? 'ok' : 'warn'}>{data.imported ? 'Imported' : 'Not imported'}</span>
      <span class="muted">Managed file</span>
      <span class="path">{data.managedFile}</span>
      <span class="count muted">{plural(data.sites.length, 'site')}, {enabledCount} enabled</span>
      <span class="note-line muted">Sites already in your Caddyfile are not listed here.</span>
    </section>

    {#if notice}<p class="notice" role="status">{notice}</p>{/if}
    {#if failure}
      <div class="failure" role="alert"><p>{failure}</p></div>
    {/if}

    <div class="layout">
      <section class="list" aria-label="Managed sites">
        {#if data.sites.length === 0}
          <p class="empty">No managed sites yet</p>
        {:else}
          <ul class="sites" aria-label="Managed sites">
            {#each data.sites as site (site.id)}
              <li class:off={!site.enabled}>
                <div class="row">
                  <div class="body">
                    <div class="nm">{site.hostname}</div>
                    <div class="sub">{siteSub(site)}</div>
                  </div>
                  <div class="meta">
                    <span class="tag" data-kind={site.kind}>{site.kind === 'redirect' ? 'Redirect' : 'Proxy'}</span>
                    <span class="muted tls">{tlsLabel(site.tls)}</span>
                    {#if isProtected(site)}<span class="tag" data-kind="auth" title="Password for {site.basicAuth.user}">Basic auth, {site.basicAuth.user}</span>{/if}
                    <button
                      type="button"
                      class="switch"
                      aria-pressed={site.enabled}
                      aria-label="{site.enabled ? 'Disable' : 'Enable'} {site.hostname}"
                      disabled={busy}
                      onclick={() => toggle(site)}
                    ></button>
                    <button type="button" class="ghost" aria-label="Edit {site.hostname}" disabled={busy} onclick={() => edit(site)}>Edit</button>
                    <button type="button" class="ghost danger" aria-label="Delete {site.hostname}" disabled={busy} onclick={() => (confirming = site.id)}>Delete</button>
                  </div>
                </div>
                {#if confirming === site.id}
                  <div class="confirm">
                    <p>Delete {site.hostname}?</p>
                    <button type="button" class="primary" disabled={busy} onclick={() => remove(site)}>Confirm delete</button>
                    <button type="button" onclick={() => (confirming = null)}>Cancel</button>
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      <section class="formpanel" aria-label="Site form">
        {#key editing.key}
          <SiteWizard
            site={editing.site}
            accessLog={data.accessLog}
            {previewDelay}
            onsaved={(result) => saved(result, editing.site)}
            oncancel={startOver}
          />
        {/key}
      </section>
    </div>
  {/if}

  <section class="running" aria-label="Running in Caddy">
    <h2>Running in Caddy</h2>
    {#if runningError}
      <p class="note">Could not read the sites Caddy is running: {runningError}</p>
    {:else if running === null}
      <p class="empty">Loading…</p>
    {:else if running.sites.length === 0}
      <p class="empty">Caddy is running no sites</p>
    {:else}
      <ul class="sites">
        {#each running.sites as site}
          <li>
            <div class="row">
              <div class="body">
                <div class="nm">{site.host || '(any host)'}</div>
                <div class="sub">
                  {#each site.upstreams as upstream, i}{i > 0 ? ', ' : ''}{upstream.address}{:else}No upstream{/each}
                </div>
              </div>
              <div class="meta">
                <span class="muted tls">{site.server}</span>
                <span class="tag" data-kind={isManaged(site.host) ? 'managed' : 'caddyfile'}>{isManaged(site.host) ? 'managed' : 'Caddyfile'}</span>
              </div>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<style>
  .pagehead {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1.5rem;
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
    margin: 0 0 0.5rem;
    font: 600 1.3rem / 1.2 var(--display);
    letter-spacing: -0.02em;
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
  .muted,
  .empty {
    color: var(--muted);
  }
  .error {
    color: var(--bad);
  }
  .note {
    color: var(--warn);
  }

  /* the managed file, as a strip under the title */
  .file {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 1rem;
    padding: 0.9rem 0;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
    margin-bottom: 2.25rem;
  }
  .file .path {
    min-width: 0;
    overflow-wrap: anywhere;
    font: 400 0.85rem / 1.4 var(--mono);
  }
  .file .count {
    margin-left: auto;
    font-size: 0.85rem;
  }
  .file .note-line {
    font-size: 0.85rem;
  }
  .tag {
    padding: 0.05rem 0.65rem;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 0.75rem;
    font-weight: 500;
    white-space: nowrap;
  }
  .tag[data-state='ok'],
  .tag[data-kind='managed'] {
    background: var(--ok-soft);
    color: var(--ok);
  }
  .tag[data-state='warn'],
  .tag[data-kind='redirect'] {
    background: var(--warn-soft);
    color: var(--warn);
  }
  .tag[data-kind='proxy'] {
    background: var(--info-soft);
    color: var(--info);
  }

  .setup {
    margin-bottom: 1.5rem;
    padding: 1rem 1.25rem;
    border-left: 3px solid var(--warn);
    background: var(--warn-soft);
  }
  .setup p {
    margin: 0 0 0.5rem;
  }
  .notice {
    padding: 0.6rem 0.9rem;
    border-left: 3px solid var(--ok);
    border-radius: var(--radius);
    background: var(--ok-soft);
  }
  .failure {
    padding: 0.6rem 0.9rem;
    border-left: 3px solid var(--bad);
    border-radius: var(--radius);
    background: var(--bad-soft);
  }
  .failure p {
    margin: 0;
    color: var(--bad);
  }

  .layout {
    display: flex;
    flex-wrap: wrap;
    gap: 3rem;
    align-items: flex-start;
  }
  .list {
    flex: 2 1 34rem;
    min-width: 0;
  }
  .formpanel {
    flex: 1 1 25rem;
    min-width: 0;
    padding: 1.5rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius-lg);
    background: linear-gradient(155deg, var(--accent-soft), transparent 52%), var(--surface);
  }

  /* the sites: rows with hairlines, a name over a line in mono, and what the site is on the right */
  .sites {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .sites li {
    border-bottom: 1px solid var(--border);
  }
  .sites li:last-child {
    border-bottom: 0;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 1rem;
    padding: 1rem 0;
  }
  .body {
    flex: 1 1 auto;
    min-width: 0;
  }
  .nm {
    overflow: hidden;
    font: 600 1.15rem / 1.2 var(--display);
    letter-spacing: -0.015em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sub {
    margin-top: 0.2rem;
    overflow: hidden;
    color: var(--muted);
    font: 400 0.8rem / 1.4 var(--mono);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  li.off .nm,
  li.off .sub {
    opacity: 0.55;
  }
  .meta {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.6rem;
  }
  .tls {
    font-size: 0.85rem;
  }
  .ghost {
    padding: 0.1rem 0.1rem;
    border-color: transparent;
    border-bottom: 2px solid var(--accent);
    border-radius: 0;
    background: transparent;
    font-size: 0.85rem;
  }
  .ghost:hover {
    color: var(--accent);
  }
  .ghost.danger {
    border-bottom-color: var(--line2);
  }
  .ghost.danger:hover {
    border-bottom-color: var(--bad);
    color: var(--bad);
  }
  /* the enable switch: a button that is pressed when the site is on */
  .switch {
    position: relative;
    width: 2.6rem;
    height: 1.5rem;
    padding: 0;
    border: 1px solid var(--line2);
    border-radius: 999px;
    background: var(--surface-2);
  }
  .switch::after {
    content: '';
    position: absolute;
    top: 0.18rem;
    left: 0.2rem;
    width: 1.1rem;
    height: 1.1rem;
    border-radius: 50%;
    background: var(--muted);
    transition: transform 0.12s;
  }
  .switch[aria-pressed='true'] {
    border-color: transparent;
    background: var(--accent);
  }
  .switch[aria-pressed='true']::after {
    background: var(--accent-text);
    transform: translateX(1.1rem);
  }
  .confirm {
    margin: 0 0 1rem;
    padding: 0.6rem 0.8rem;
    border-radius: var(--radius);
    background: var(--surface-2);
  }
  .confirm p {
    margin: 0 0 0.5rem;
  }

  .running {
    margin-top: 3.5rem;
  }
  .running .sites {
    border-top: 1px solid var(--border);
  }
  @media (max-width: 48rem) {
    .row {
      flex-wrap: wrap;
    }
    .meta {
      width: 100%;
      justify-content: flex-start;
    }
    .nm,
    .sub {
      overflow-wrap: anywhere;
      white-space: normal;
    }
  }
</style>
