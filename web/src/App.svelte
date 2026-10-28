<script>
  import { ApiError, getConfig, getSites, getStatus, getSummary, getTraffic } from './lib/api.js'
  import { LogStream } from './lib/logstream.js'
  import { startPolling } from './lib/poller.js'
  import { TABS, tabFromPath } from './lib/router.js'
  import { applyTheme, loadTheme, saveTheme, toggledTheme } from './lib/theme.js'
  import ConfigView from './screens/ConfigView.svelte'
  import Dashboard from './screens/Dashboard.svelte'
  import LogView from './screens/LogView.svelte'

  const POLL_MS = 5000
  const LOG_URL = '/api/logs/stream?backlog=100'

  // Line icons on a 24x24 grid, drawn with the current colour. They are fixed strings, never user input.
  const ICONS = {
    dashboard: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
    config: '<path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h12M20 18h0"/><circle cx="16" cy="6" r="2"/><circle cx="8" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>',
    logs: '<path d="M8 6h12M8 12h12M8 18h12"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/>',
  }

  // Reading localStorage itself can throw (blocked site data), so the theme gets a store that never does.
  function themeStorage() {
    try {
      return window.localStorage
    } catch {
      return { getItem: () => null, setItem: () => {} }
    }
  }

  const systemDark = () => window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false

  let theme = $state(loadTheme(themeStorage()))
  applyTheme(document.documentElement, theme)
  const nextTheme = $derived(toggledTheme(theme, systemDark()))

  function switchTheme() {
    theme = nextTheme
    applyTheme(document.documentElement, theme)
    saveTheme(themeStorage(), theme)
  }

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
  let summary = $state(null)
  let traffic = $state(null)
  let historyOff = $state(false)
  let dashboardError = $state('')

  $effect(() => {
    if (tab !== 'dashboard') return
    return startPolling(async () => {
      try {
        status = await getStatus()
        // The sites need a reachable Caddy, and /api/sites answers 502 when it is not.
        sites = status.caddy.reachable ? await getSites() : null
        try {
          ;[summary, traffic] = await Promise.all([getSummary('24h'), getTraffic('hour')])
          historyOff = false
        } catch (err) {
          // Without a history the routes are not there at all: that is a setting, not a failure.
          if (!(err instanceof ApiError && err.status === 404)) throw err
          summary = traffic = null
          historyOff = true
        }
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
  <div class="bar">
    <span class="brand">caddy-view</span>
    <nav aria-label="Main">
      {#each TABS as t}
        <a href={t.path} aria-current={tab === t.id ? 'page' : undefined} onclick={(e) => navigate(e, t.path)}>
          <svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html ICONS[t.id]}</svg>
          <span>{t.label}</span>
        </a>
      {/each}
    </nav>
    <button class="theme" type="button" aria-label="Switch to {nextTheme} theme" title="Switch to {nextTheme} theme" onclick={switchTheme}>
      <svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html nextTheme === 'dark' ? ICONS.moon : ICONS.sun}</svg>
    </button>
  </div>
</header>

<main>
  {#if tab === 'dashboard'}
    <Dashboard {status} {sites} {summary} {traffic} {historyOff} loadError={dashboardError} />
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
    padding: 0 1rem;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    max-width: 72rem;
    margin: 0 auto;
    padding: 0.4rem 0.5rem 0.4rem 1rem;
    border-radius: 999px;
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  .brand {
    font-weight: 600;
    letter-spacing: -0.01em;
  }
  nav {
    display: flex;
    flex: 1;
    justify-content: center;
    gap: 0.15rem;
    min-width: 0;
  }
  a {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.4rem 0.9rem;
    border-radius: 999px;
    color: var(--muted);
    font-size: 0.9rem;
    text-decoration: none;
  }
  a:hover {
    color: var(--text);
  }
  a[aria-current='page'] {
    background: var(--surface-2);
    color: var(--text);
    font-weight: 600;
  }
  .icon {
    flex: none;
    width: 1.05rem;
    height: 1.05rem;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .theme {
    display: grid;
    place-items: center;
    width: 2.2rem;
    height: 2.2rem;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: var(--surface-2);
    color: var(--muted);
  }
  .theme:hover {
    color: var(--text);
  }
  main {
    max-width: 72rem;
    margin: 2rem auto;
    padding: 0 1rem;
  }

  @media (max-width: 40rem) {
    .brand {
      display: none;
    }
    /* Only the icon shows, but the tab keeps its name for screen readers. */
    a span {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
    }
    .bar {
      padding-left: 0.5rem;
    }
    a {
      padding: 0.55rem 0.8rem;
    }
  }
</style>
