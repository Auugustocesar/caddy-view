<script>
  import { getConfig, getSites, getStatus } from './lib/api.js'
  import { LogStream } from './lib/logstream.js'
  import { startPolling } from './lib/poller.js'
  import { TABS, tabFromPath } from './lib/router.js'
  import ConfigView from './screens/ConfigView.svelte'
  import Dashboard from './screens/Dashboard.svelte'
  import LogView from './screens/LogView.svelte'

  const POLL_MS = 5000
  const LOG_URL = '/api/logs/stream?backlog=100'

  let path = $state(window.location.pathname)
  const tab = $derived(tabFromPath(path))

  function navigate(event, target) {
    // Let the browser handle "open in a new tab" and similar.
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    if (target !== path) window.history.pushState({}, '', target)
    path = target
  }

  // Dashboard: poll while the tab is open.
  let status = $state(null)
  let sites = $state(null)
  let dashboardError = $state('')

  $effect(() => {
    if (tab !== 'dashboard') return
    return startPolling(async () => {
      try {
        status = await getStatus()
        // The sites need a reachable Caddy, and /api/sites answers 502 when it is not.
        sites = status.caddy.reachable ? await getSites() : null
        dashboardError = ''
      } catch (err) {
        dashboardError = err.message
      }
    }, POLL_MS)
  })

  // Config: read once each time the tab is opened.
  let config = $state(null)
  let configError = $state('')

  $effect(() => {
    if (tab !== 'config') return
    let current = true
    getConfig().then(
      (value) => {
        if (!current) return
        config = value
        configError = ''
      },
      (err) => {
        if (current) configError = err.message
      },
    )
    return () => (current = false)
  })

  // Logs: the stream is open only while the tab is.
  let logs = $state({ lines: [], status: 'connecting', reason: '', paused: false, heldCount: 0 })
  let filter = $state('')
  let stream = $state.raw(null)

  $effect(() => {
    if (tab !== 'logs') return
    const sync = () => {
      logs = {
        lines: current.lines,
        status: current.status,
        reason: current.reason,
        paused: current.paused,
        heldCount: current.heldCount,
      }
    }
    const current = new LogStream(LOG_URL, { onChange: sync })
    stream = current
    current.start()
    sync()
    return () => {
      current.stop()
      stream = null
    }
  })
</script>

<svelte:window onpopstate={() => (path = window.location.pathname)} />

<header>
  <nav aria-label="Main">
    <span class="brand">caddy-view</span>
    {#each TABS as t}
      <a href={t.path} aria-current={tab === t.id ? 'page' : undefined} onclick={(e) => navigate(e, t.path)}>{t.label}</a>
    {/each}
  </nav>
</header>

<main>
  {#if tab === 'dashboard'}
    <Dashboard {status} {sites} loadError={dashboardError} />
  {:else if tab === 'config'}
    <ConfigView {config} loadError={configError} />
  {:else}
    <LogView
      {...logs}
      {filter}
      onpause={() => stream?.pause()}
      onresume={() => stream?.resume()}
      onclear={() => stream?.clear()}
      onfilter={(text) => (filter = text)}
    />
  {/if}
</main>

<style>
  header {
    position: sticky;
    top: 0.75rem;
    z-index: 1;
    display: flex;
    justify-content: center;
    padding: 0 1rem;
  }
  nav {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem;
    padding: 0.35rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface);
    box-shadow: 0 4px 16px rgb(0 0 0 / 0.12);
  }
  .brand {
    padding: 0 0.75rem;
    font-weight: 700;
  }
  a {
    padding: 0.35rem 0.9rem;
    border-radius: 999px;
    color: var(--muted);
    text-decoration: none;
  }
  a:hover {
    color: var(--text);
  }
  a[aria-current='page'] {
    background: var(--accent);
    color: var(--accent-text);
  }
  main {
    max-width: 72rem;
    margin: 1.5rem auto;
    padding: 0 1rem;
  }
</style>
