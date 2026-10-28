import { fireEvent, render, screen } from '@testing-library/svelte'
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
  switch (url) {
    case '/api/status':
      return Promise.resolve(json({ caddy: { reachable: true, endpoint: 'http://localhost:2019' } }))
    case '/api/sites':
      return Promise.resolve(json({ sites: [], upstreamsAvailable: true }))
    case '/api/config':
      return Promise.resolve(json(config))
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
  it('has exactly the three tabs, as links', () => {
    openAt('/')
    const nav = screen.getByRole('navigation')
    const links = Array.from(nav.querySelectorAll('a')).map((a) => [a.textContent.trim(), a.getAttribute('href')])
    expect(links).toEqual([
      ['Dashboard', '/'],
      ['Config', '/config'],
      ['Logs', '/logs'],
    ])
  })

  it.each([
    ['/', 'Dashboard'],
    ['/config', 'Config'],
    ['/logs', 'Logs'],
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
