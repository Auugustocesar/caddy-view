<script>
  import { ApiError, getAttention, getConfig, getSites, getStatus, getSummary, getTraffic, reportUnauthorized } from './lib/api.js'
  import { LogStream } from './lib/logstream.js'
  import { startPolling } from './lib/poller.js'
  import { TABS, tabFromPath } from './lib/router.js'
  import Segmented from './components/Segmented.svelte'
  import { applyTheme, loadTheme, saveTheme, toggledTheme } from './lib/theme.js'
  import CaddyfileView from './screens/CaddyfileView.svelte'
  import ConfigView from './screens/ConfigView.svelte'
  import SitesView from './screens/SitesView.svelte'
  import Dashboard from './screens/Dashboard.svelte'
  import LogHistory from './screens/LogHistory.svelte'
  import LogView from './screens/LogView.svelte'
  import MetricsView from './screens/MetricsView.svelte'

  // user is the name shown next to the sign out button; without a login there is neither.
  let { user = null, onlogout = () => {} } = $props()

  const POLL_MS = 5000
  const LOG_URL = '/api/logs/stream?backlog=100'

  // Line icons on a 24x24 grid, drawn with the current colour. They are fixed strings, never user input.
  const ICONS = {
    dashboard: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
    config: '<path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h12M20 18h0"/><circle cx="16" cy="6" r="2"/><circle cx="8" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>',
    metrics: '<path d="M5 20V11M12 20V4M19 20v-6"/>',
    logs: '<path d="M8 6h12M8 12h12M8 18h12"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/>',
    sites: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
    caddyfile: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13l-2 2 2 2M15 13l2 2-2 2"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/>',
    signout: '<path d="M9 4H5a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h4M16 8l4 4-4 4M20 12H9"/>',
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
  let attention = $state(null)
  let historyOff = $state(false)
  let dashboardError = $state('')
  let range = $state('24h')

  // The traffic of a range: by the minute for an hour, by the hour for a day, and for a week by the hour from seven days ago.
  function trafficFor(window) {
    if (window === '1h') return getTraffic('minute')
    if (window === '7d') return getTraffic('hour', { from: new Date(Date.now() - 7 * 24 * 3600 * 1000).toISOString() })
    return getTraffic('hour')
  }

  $effect(() => {
    if (tab !== 'dashboard') return
    const window = range
    return startPolling(async () => {
      try {
        status = await getStatus()
        // The sites need a reachable Caddy, and /api/sites answers 502 when it is not.
        sites = status.caddy.reachable ? await getSites() : null
        try {
          ;[summary, traffic] = await Promise.all([getSummary(window), trafficFor(window)])
          historyOff = false
          // What needs attention is a help, not the page: when it cannot be read the figures still show.
          attention = await getAttention(window).then(
            (body) => body.items,
            () => null,
          )
        } catch (err) {
          // Without a history the routes are not there at all: that is a setting, not a failure.
          if (!(err instanceof ApiError && err.status === 404)) throw err
          summary = traffic = attention = null
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

  // Logs: the stream is open only while the live tail is showing. A new visit to the tab starts on the live tail.
  const LOG_VIEWS = [
    { value: 'live', label: 'Live' },
    { value: 'history', label: 'History' },
  ]
  let logsView = $state('live')
  $effect(() => {
    if (tab !== 'logs') logsView = 'live'
  })
  let logs = $state({ lines: [], status: 'connecting', reason: '', paused: false, heldCount: 0 })
  let filter = $state('')
  let stream = $state.raw(null)

  $effect(() => {
    if (tab !== 'logs' || logsView !== 'live') return
    const sync = () => {
      logs = {
        lines: current.lines,
        status: current.status,
        reason: current.reason,
        paused: current.paused,
        heldCount: current.heldCount,
      }
    }
    const current = new LogStream(LOG_URL, { onChange: sync, onUnauthorized: reportUnauthorized })
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
    {#if user}
      <span class="user">{user}</span>
      <button class="signout" type="button" onclick={onlogout}>
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html ICONS.signout}</svg>
        <span>Sign out</span>
      </button>
    {/if}
    <button class="theme" type="button" aria-label="Switch to {nextTheme} theme" title="Switch to {nextTheme} theme" onclick={switchTheme}>
      <svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html nextTheme === 'dark' ? ICONS.moon : ICONS.sun}</svg>
    </button>
  </div>
</header>

<main>
  {#if tab === 'dashboard'}
    <Dashboard {status} {sites} {summary} {traffic} {attention} {range} onrange={(value) => (range = value)} {historyOff} loadError={dashboardError} />
  {:else if tab === 'sites'}
    <SitesView />
  {:else if tab === 'caddyfile'}
    <CaddyfileView />
  {:else if tab === 'config'}
    <ConfigView {config} loadError={configError} />
  {:else if tab === 'metrics'}
    <MetricsView />
  {:else}
    <div class="view-switch">
      <Segmented label="Logs view" options={LOG_VIEWS} value={logsView} onchange={(view) => (logsView = view)} />
    </div>
    {#if logsView === 'live'}
      <LogView
        {...logs}
        {filter}
        onpause={() => stream?.pause()}
        onresume={() => stream?.resume()}
        onclear={() => stream?.clear()}
        onfilter={(text) => (filter = text)}
      />
    {:else}
      <LogHistory />
    {/if}
  {/if}
</main>

<style>
  header {
    position: sticky;
    top: 0;
    z-index: 3;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  .bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem 2rem;
    max-width: 80rem;
    margin: 0 auto;
    padding: 1.1rem 2rem;
  }
  .brand {
    font: 700 1.3rem / 1 var(--display);
    letter-spacing: -0.025em;
  }
  nav {
    display: flex;
    flex: 1;
    flex-wrap: wrap;
    justify-content: center;
    gap: 0.25rem 1.75rem;
    min-width: 0;
  }
  a {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.35rem 0;
    border-bottom: 2px solid transparent;
    color: var(--muted);
    font-weight: 500;
    text-decoration: none;
  }
  a:hover {
    color: var(--text);
  }
  a[aria-current='page'] {
    border-bottom-color: var(--accent);
    color: var(--text);
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
  /* the tabs are words on a wide screen; the icons are for a narrow one */
  nav .icon {
    display: none;
  }
  .theme {
    display: grid;
    place-items: center;
    width: 2.1rem;
    height: 2.1rem;
    padding: 0;
    border-radius: 50%;
    color: var(--muted);
  }
  .theme:hover {
    color: var(--text);
  }
  .user {
    color: var(--muted);
    font-size: 0.9rem;
  }
  .signout {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.2rem 0;
    border: 0;
    border-bottom: 2px solid var(--accent);
    border-radius: 0;
    color: var(--text);
    font-size: 0.9rem;
  }
  .signout .icon {
    display: none;
  }
  .view-switch {
    margin-bottom: 1.25rem;
  }
  main {
    max-width: 80rem;
    margin: 2.5rem auto 4rem;
    padding: 0 2rem;
  }

  @media (max-width: 48rem) {
    .bar {
      padding: 0.8rem 1rem;
      gap: 0.5rem 1rem;
    }
    main {
      margin-top: 1.5rem;
      padding: 0 1rem;
    }
    nav {
      order: 3;
      flex-basis: 100%;
      justify-content: flex-start;
      overflow-x: auto;
      flex-wrap: nowrap;
      gap: 1.25rem;
    }
    nav a {
      flex: none;
    }
    .brand {
      flex: 1;
    }
  }
</style>
