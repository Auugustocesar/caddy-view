<script>
  import SiteWizard from '../components/SiteWizard.svelte'
  import { addImport, deleteSite, getManagedSites, getSites, updateSite } from '../lib/api.js'
  import { tidyCaddyError } from '../lib/caddy-error.js'
  import { describeSite, formFromSite, payloadFromForm } from '../lib/site-form.js'

  /** @type {{previewDelay?: number}} */
  let { previewDelay = 400 } = $props()

  /** @type {'loading'|'off'|'ready'|'failed'} */
  let phase = $state('loading')
  let loadError = $state('')
  /** @type {{sites: Array<any>, managedFile: string, imported: boolean, accessLog: boolean}} */
  let data = $state({ sites: [], managedFile: '', imported: true, accessLog: false })
  /** @type {{sites: Array<{server: string, host: string, upstreams: Array<{address: string}>}>} | null} */
  let running = $state(null)
  let runningError = $state('')

  /** @type {{site: object | null} | null} the wizard that is open: for a new site, or for one that exists */
  let wizard = $state(null)
  /** @type {string | null} */
  let confirming = $state(null)
  let notice = $state('')
  let failure = $state('')
  let busy = $state(false)

  const enabledHosts = $derived(new Set(data.sites.filter((s) => s.enabled).map((s) => s.hostname.toLowerCase())))
  const isManaged = (host) => enabledHosts.has(host.toLowerCase())

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

  async function saved(result, existing) {
    wizard = null
    notice = `${existing ? 'Saved' : 'Added'} ${result.site?.hostname ?? existing?.hostname ?? 'the site'} (revision ${result.revision.number})`
    failure = ''
    await refresh()
  }

  function openWizard(site) {
    wizard = { site }
    confirming = null
    notice = ''
    failure = ''
  }
</script>

<header class="page">
  <h1>Sites</h1>
  <p class="subtitle">The sites caddy-view manages, and what Caddy runs.</p>
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
      <section class="setup card" aria-label="Setup">
        <p>
          The Caddyfile does not import the file caddy-view writes its sites to, so a site added here would never be served.
        </p>
        <p><code>{data.managedFile}</code></p>
        <button type="button" class="primary" onclick={addTheImport} disabled={busy}>Add the import</button>
      </section>
    {/if}

    {#if notice}<p class="notice" role="status">{notice}</p>{/if}
    {#if failure}
      <div class="failure" role="alert"><p>{failure}</p></div>
    {/if}

    <section aria-label="Managed sites">
      <div class="heading">
        <h2>Managed sites</h2>
        {#if !wizard}
          <button type="button" class="primary" onclick={() => openWizard(null)} disabled={!data.imported || busy}>Add site</button>
        {/if}
      </div>

      {#if wizard}
        {#key wizard.site?.id ?? 'new'}
          <SiteWizard
            site={wizard.site}
            accessLog={data.accessLog}
            {previewDelay}
            onsaved={(result) => saved(result, wizard?.site)}
            oncancel={() => (wizard = null)}
          />
        {/key}
      {/if}

      {#if data.sites.length === 0}
        <p class="empty">No managed sites yet</p>
      {:else}
        <div class="card flush">
          <table class="stack" aria-label="Managed sites">
            <thead>
              <tr><th>Hostname</th><th>What it does</th><th>TLS</th><th>State</th><th><span class="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {#each data.sites as site (site.id)}
                <tr>
                  <td class="host" data-label="Hostname">{site.hostname}</td>
                  <td data-label="What it does">{describeSite(site)}</td>
                  <td data-label="TLS">{site.tls}</td>
                  <td data-label="State"><span class="badge" data-state={site.enabled ? 'enabled' : 'disabled'}>{site.enabled ? 'Enabled' : 'Disabled'}</span></td>
                  <td class="actions"><div class="buttons">
                    <button type="button" aria-label="Edit {site.hostname}" disabled={busy} onclick={() => openWizard(site)}>Edit</button>
                    <button type="button" aria-label="{site.enabled ? 'Disable' : 'Enable'} {site.hostname}" disabled={busy} onclick={() => toggle(site)}>
                      {site.enabled ? 'Disable' : 'Enable'}
                    </button>
                    <button type="button" aria-label="Delete {site.hostname}" disabled={busy} onclick={() => (confirming = site.id)}>Delete</button>
                    </div>
                  </td>
                </tr>
                {#if confirming === site.id}
                  <tr class="confirm">
                    <td colspan="5">
                      <p>Delete {site.hostname}?</p>
                      <button type="button" class="primary" disabled={busy} onclick={() => remove(site)}>Confirm delete</button>
                      <button type="button" onclick={() => (confirming = null)}>Cancel</button>
                    </td>
                  </tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  {/if}

  <section aria-label="Running in Caddy">
    <h2>Running in Caddy</h2>
    {#if runningError}
      <p class="note">Could not read the sites Caddy is running: {runningError}</p>
    {:else if running === null}
      <p class="empty">Loading…</p>
    {:else if running.sites.length === 0}
      <p class="empty">Caddy is running no sites</p>
    {:else}
      <div class="card flush">
        <table class="stack">
          <thead>
            <tr><th>Site</th><th>Server</th><th>Upstreams</th><th>Defined in</th></tr>
          </thead>
          <tbody>
            {#each running.sites as site}
              <tr>
                <td class="host" data-label="Site">{site.host || '(any host)'}</td>
                <td data-label="Server">{site.server}</td>
                <td data-label="Upstreams">
                  {#each site.upstreams as upstream}<div><code>{upstream.address}</code></div>{:else}<span class="detail">No upstream</span>{/each}
                </td>
                <td data-label="Defined in"><span class="badge" data-state={isManaged(site.host) ? 'managed' : 'caddyfile'}>{isManaged(site.host) ? 'managed' : 'Caddyfile'}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}

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
    margin: 0;
    font-size: 1.05rem;
    font-weight: 500;
  }
  section {
    margin-top: 2rem;
  }
  .subtitle {
    margin: 0.25rem 0 0;
    color: var(--muted);
  }
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.75rem;
  }
  .card {
    border-radius: var(--radius-lg);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .card.flush {
    padding: 0.25rem 0.5rem;
    overflow-x: auto;
  }
  .setup {
    margin-top: 0;
    padding: 1rem 1.25rem;
    border-left: 4px solid var(--warn);
  }
  .setup p {
    margin: 0 0 0.5rem;
  }
  .notice {
    padding: 0.6rem 0.9rem;
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow);
    border-left: 4px solid var(--ok);
  }
  .failure {
    padding: 0.6rem 0.9rem;
    border-radius: var(--radius);
    background: var(--surface);
    box-shadow: var(--shadow);
    border-left: 4px solid var(--bad);
  }
  .failure p {
    margin: 0;
    color: var(--bad);
  }
  .error {
    color: var(--bad);
  }
  .note {
    color: var(--warn);
  }
  .empty,
  .detail {
    color: var(--muted);
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
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    text-align: left;
    padding: 0.65rem 0.75rem;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  th {
    color: var(--muted);
    font-weight: 600;
    font-size: 0.75rem;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }
  .host {
    font-weight: 600;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
    justify-content: flex-end;
  }
  .actions {
    white-space: nowrap;
  }
  .badge {
    padding: 0.05rem 0.55rem;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 0.75rem;
  }
  .badge[data-state='enabled'],
  .badge[data-state='managed'] {
    color: var(--ok);
  }
  .confirm td {
    background: var(--surface-2);
  }
  .confirm p {
    margin: 0 0 0.5rem;
  }
  /* On a phone each row becomes a card: the table is wider than the screen, and the actions would be out of sight. */
  @media (max-width: 40rem) {
    .stack thead {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
    }
    .stack,
    .stack tbody,
    .stack tr,
    .stack td {
      display: block;
    }
    .stack tr {
      padding: 0.75rem 0.5rem;
      border-bottom: 1px solid var(--border);
    }
    .stack tbody tr:last-child {
      border-bottom: 0;
    }
    .stack td {
      display: flex;
      gap: 0.75rem;
      padding: 0.15rem 0.25rem;
      border: 0;
    }
    .stack td[data-label]::before {
      content: attr(data-label);
      flex: none;
      width: 6.5rem;
      padding-top: 0.2rem;
      color: var(--muted);
      font-size: 0.7rem;
      letter-spacing: 0.06em;
      text-transform: uppercase;
    }
    .stack .buttons {
      justify-content: flex-start;
    }
    .stack .host {
      font-size: 1.05rem;
    }
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
</style>
