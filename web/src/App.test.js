import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.svelte'

const config = { apps: { http: {} } }
const summary = {
  window: '24h',
  requests: 12,
  requestsPerSecond: 0,
  errors5xx: 0,
  errorRate: 0,
  avgMs: 5,
  p95Ms: 9,
  bytes: 2048,
  status: {},
  inFlight: null,
  upstreams: null,
}
const traffic = {
  resolution: 'hour',
  bucketSeconds: 3600,
  buckets: [{ start: new Date(2026, 9, 28, 9).toISOString(), requests: 12 }],
}

function json(body) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

// Routes the API calls the screens make.
function fakeFetch(url) {
  // The Metrics tab and the log history ask with a range; the Dashboard asks for the resolution alone.
  if (url.startsWith('/api/traffic?resolution=') && url.includes('&from=')) return Promise.resolve(json(traffic))
  if (url.startsWith('/api/hosts?')) return Promise.resolve(json({ hosts: [{ host: 'app.example.com', requests: 12, bytes: 2048, errors5xx: 0, status: {}, avgMs: 5, p95Ms: 9, maxMs: 9 }] }))
  if (url.startsWith('/api/logs?')) return Promise.resolve(json({ entries: [{ id: 1, time: '2026-10-28T11:00:00Z', host: 'app.example.com', method: 'GET', path: '/from-the-history', status: 200, durationMs: 4, bytes: 10, clientIp: '10.0.0.1' }], next: null }))
  switch (url) {
    case '/api/status':
      return Promise.resolve(json({ caddy: { reachable: true, endpoint: 'http://localhost:2019' } }))
    case '/api/sites':
      return Promise.resolve(json({ sites: [], upstreamsAvailable: true }))
    case '/api/config':
      return Promise.resolve(json(config))
    case '/api/system':
      return Promise.resolve(json({ editing: { enabled: true } }))
    case '/api/caddyfile':
      return Promise.resolve(json({ content: 'example.com {\n}\n', sha256: 'a'.repeat(64) }))
    case '/api/revisions?limit=20':
      return Promise.resolve(json({ revisions: [], next: null }))
    case '/api/managed-sites':
      return Promise.resolve(json({ sites: [], managedFile: '/etc/caddy/conf.d/sites.caddy', imported: true, accessLog: false }))
    case '/api/summary?window=24h':
      return Promise.resolve(json(summary))
    case '/api/traffic?resolution=hour':
      return Promise.resolve(json(traffic))
    default:
      return Promise.reject(new Error(`unexpected request to ${url}`))
  }
}

class QuietEventSource extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  close() {}
}

function openAt(path) {
  window.history.replaceState({}, '', path)
  return render(App)
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(fakeFetch))
  vi.stubGlobal('EventSource', QuietEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
  document.documentElement.removeAttribute('data-theme')
  window.localStorage.clear()
})

describe('App navigation', () => {
  it('has exactly the six tabs, as links', () => {
    openAt('/')
    const nav = screen.getByRole('navigation')
    const links = Array.from(nav.querySelectorAll('a')).map((a) => [a.textContent.trim(), a.getAttribute('href')])
    expect(links).toEqual([
      ['Dashboard', '/'],
      ['Metrics', '/metrics'],
      ['Logs', '/logs'],
      ['Sites', '/sites'],
      ['Caddyfile', '/caddyfile'],
      ['Config', '/config'],
    ])
  })

  it.each([
    ['/', 'Dashboard'],
    ['/config', 'Config'],
    ['/metrics', 'Metrics'],
    ['/logs', 'Logs'],
    ['/sites', 'Sites'],
    ['/caddyfile', 'Caddyfile'],
    ['/unknown', 'Dashboard'],
  ])('opens %s on the %s tab', (path, label) => {
    openAt(path)
    const current = screen.getAllByRole('link').filter((a) => a.getAttribute('aria-current') === 'page')
    expect(current.map((a) => a.textContent.trim())).toEqual([label])
  })

  it('switches tab on click without reloading, and updates the address', async () => {
    openAt('/')
    await fireEvent.click(screen.getByRole('link', { name: 'Config' }))

    expect(window.location.pathname).toBe('/config')
    expect(screen.getByRole('link', { name: 'Config' }).getAttribute('aria-current')).toBe('page')
    expect(screen.getByRole('link', { name: 'Dashboard' }).getAttribute('aria-current')).toBeNull()
    expect(await screen.findByTestId('config-json')).toBeTruthy()
  })

  it('follows the browser back button', async () => {
    openAt('/')
    await fireEvent.click(screen.getByRole('link', { name: 'Logs' }))
    expect(screen.getByRole('link', { name: 'Logs' }).getAttribute('aria-current')).toBe('page')

    window.history.back()
    await vi.waitFor(() => expect(screen.getByRole('link', { name: 'Dashboard' }).getAttribute('aria-current')).toBe('page'))
  })
})

describe('App screens', () => {
  it('loads the dashboard from the API', async () => {
    openAt('/')
    expect(await screen.findByText(/Caddy is reachable/)).toBeTruthy()
    expect(await screen.findByText('No sites found')).toBeTruthy()
  })

  it('shows an error when the API cannot be read', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    openAt('/')
    expect((await screen.findByRole('alert')).textContent).toMatch(/caddy-view/i)
  })

  it('opens the managed sites on the Sites tab', async () => {
    openAt('/sites')
    expect(await screen.findByRole('heading', { level: 1, name: 'Sites' })).toBeTruthy()
    expect(await screen.findByText(/no managed sites yet/i)).toBeTruthy()
  })

  it('opens the Caddyfile editor on its tab', async () => {
    openAt('/caddyfile')
    expect(await screen.findByLabelText('Caddyfile editor')).toBeTruthy()
  })

  it('opens the log stream only on the Logs tab', async () => {
    const created = []
    vi.stubGlobal(
      'EventSource',
      class extends QuietEventSource {
        constructor(url) {
          super()
          created.push(url)
        }
      },
    )
    openAt('/')
    expect(created).toEqual([])

    await fireEvent.click(screen.getByRole('link', { name: 'Logs' }))
    expect(created).toEqual(['/api/logs/stream?backlog=100'])
  })

  it('closes the log stream when leaving the Logs tab', async () => {
    const closed = vi.fn()
    vi.stubGlobal(
      'EventSource',
      class extends QuietEventSource {
        close() {
          closed()
        }
      },
    )
    openAt('/logs')
    await fireEvent.click(screen.getByRole('link', { name: 'Dashboard' }))
    expect(closed).toHaveBeenCalledOnce()
  })

  it('opens the metrics on the Metrics tab', async () => {
    openAt('/metrics')
    expect(await screen.findByRole('heading', { level: 1, name: 'Metrics' })).toBeTruthy()
    expect(await screen.findByRole('table', { name: 'Hosts' })).toBeTruthy()
  })
})

describe('App logs view', () => {
  const trackingStream = () => {
    const state = { created: [], closed: 0 }
    vi.stubGlobal(
      'EventSource',
      class extends QuietEventSource {
        constructor(url) {
          super()
          state.created.push(url)
        }
        close() {
          state.closed++
        }
      },
    )
    return state
  }

  it('shows the live tail first, and a choice between it and the history', () => {
    trackingStream()
    openAt('/logs')
    const group = screen.getByRole('radiogroup', { name: 'Logs view' })
    expect(within(group).getAllByRole('radio').map((r) => [r.textContent.trim(), r.getAttribute('aria-checked')])).toEqual([
      ['Live', 'true'],
      ['History', 'false'],
    ])
    expect(screen.getByLabelText('Filter')).toBeTruthy()
  })

  it('shows the history search instead of the tail, and closes the stream while it does', async () => {
    const stream = trackingStream()
    openAt('/logs')
    expect(stream.created).toHaveLength(1)

    await fireEvent.click(screen.getByRole('radio', { name: 'History' }))
    expect(await screen.findByText('/from-the-history')).toBeTruthy()
    expect(screen.queryByLabelText('Filter')).toBeNull()
    expect(stream.closed).toBe(1)
    expect(screen.getByLabelText('Search path')).toBeTruthy()
  })

  it('opens the stream again when it goes back to the live tail', async () => {
    const stream = trackingStream()
    openAt('/logs')
    await fireEvent.click(screen.getByRole('radio', { name: 'History' }))
    await screen.findByText('/from-the-history')
    await fireEvent.click(screen.getByRole('radio', { name: 'Live' }))
    expect(stream.created).toHaveLength(2)
    expect(screen.getByLabelText('Filter')).toBeTruthy()
  })

  it('does not open the stream for the history when the page is opened on it', async () => {
    const stream = trackingStream()
    openAt('/logs')
    await fireEvent.click(screen.getByRole('radio', { name: 'History' }))
    await fireEvent.click(screen.getByRole('link', { name: 'Dashboard' }))
    await fireEvent.click(screen.getByRole('link', { name: 'Logs' }))
    // Back on Logs it is the live tail again, as a new visit starts.
    expect(screen.getByRole('radio', { name: 'Live' }).getAttribute('aria-checked')).toBe('true')
    expect(stream.created).toHaveLength(2)
  })
})

function failing(prefix, status, body) {
  return vi.fn((url) =>
    url.startsWith(prefix)
      ? Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
      : fakeFetch(url),
  )
}

describe('App shell', () => {
  it('gives every tab an icon that screen readers skip', () => {
    openAt('/')
    for (const link of screen.getByRole('navigation').querySelectorAll('a')) {
      expect(link.querySelector('svg[aria-hidden="true"]')).not.toBeNull()
    }
  })

  it('has a theme button that says what it will do and remembers the choice', async () => {
    openAt('/')
    await fireEvent.click(screen.getByRole('button', { name: 'Switch to dark theme' }))
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(window.localStorage.getItem('caddy-view-theme')).toBe('dark')

    await fireEvent.click(screen.getByRole('button', { name: 'Switch to light theme' }))
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(window.localStorage.getItem('caddy-view-theme')).toBe('light')
  })

  it('starts with the theme that was chosen before', () => {
    window.localStorage.setItem('caddy-view-theme', 'dark')
    openAt('/')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(screen.getByRole('button', { name: 'Switch to light theme' })).toBeTruthy()
  })
})

describe('App dashboard history', () => {
  it('shows the totals and the chart from the history', async () => {
    openAt('/')
    const requests = await screen.findByRole('group', { name: 'Requests' })
    expect(requests.textContent).toContain('12')
    expect((await screen.findByRole('img')).getAttribute('aria-label')).toContain('Requests per hour')
  })

  it('says the history is off when its routes are not there', async () => {
    vi.stubGlobal('fetch', failing('/api/summary', 404, { error: 'not found', code: 'not-found' }))
    openAt('/')
    expect(await screen.findByText(/traffic history is off/i)).toBeTruthy()
    expect(await screen.findByText(/Caddy is reachable/)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows an error when the history fails for another reason', async () => {
    vi.stubGlobal('fetch', failing('/api/summary', 500, { error: 'the store is broken' }))
    openAt('/')
    expect((await screen.findByRole('alert')).textContent).toContain('the store is broken')
  })
})
