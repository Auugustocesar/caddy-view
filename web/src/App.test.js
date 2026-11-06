import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.svelte'
import { setUnauthorizedHandler } from './lib/api.js'

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

class QuietEventSource extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  close() {}
}

/**
 * A server with a login: `session` says whether the cookie is valid, and /api/login with the right password makes it
 * valid. Everything else answers 401 without a session and a plain empty answer with one.
 */
function server({ loggedIn = false, loginAnswer } = {}) {
  const state = { loggedIn, calls: [] }
  const fetchMock = vi.fn((url, init = {}) => {
    state.calls.push([url, init.method ?? 'GET'])
    if (url === '/api/login') {
      if (loginAnswer) return Promise.resolve(loginAnswer)
      state.loggedIn = true
      return Promise.resolve(json({ user: { username: 'admin' } }))
    }
    if (url === '/api/logout') {
      state.loggedIn = false
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    // The session ended just as the log stream asked for its lines.
    if (state.streamRefused && url.startsWith('/api/logs/stream')) return Promise.resolve(json({ error: 'login required' }, 401))
    if (!state.loggedIn) return Promise.resolve(json({ error: 'login required' }, 401))
    if (url === '/api/me') return Promise.resolve(json({ user: { username: 'admin' } }))
    if (url === '/api/status') return Promise.resolve(json({ caddy: { reachable: true, endpoint: 'http://localhost:2019' } }))
    if (url === '/api/sites') return Promise.resolve(json({ sites: [], upstreamsAvailable: true }))
    return Promise.resolve(json({ error: 'not found' }, 404))
  })
  vi.stubGlobal('fetch', fetchMock)
  return state
}

beforeEach(() => vi.stubGlobal('EventSource', QuietEventSource))

afterEach(() => {
  vi.unstubAllGlobals()
  setUnauthorizedHandler(null)
  window.history.replaceState({}, '', '/')
  window.localStorage.clear()
})

describe('App without a login', () => {
  it('opens the app when the server has no login routes', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url) => Promise.resolve(url === '/api/status' ? json({ caddy: { reachable: true, endpoint: 'e' } }) : json({ error: 'not found' }, 404))),
    )
    render(App)
    expect(await screen.findByRole('navigation')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Sign out' })).toBeNull()
    expect(screen.queryByLabelText('Password')).toBeNull()
  })
})

describe('App with a login', () => {
  it('shows nothing of the app while it checks the session', () => {
    server({ loggedIn: true })
    render(App)
    expect(screen.queryByRole('navigation')).toBeNull()
  })

  it('shows the login screen, and no tab, when there is no session', async () => {
    server()
    render(App)
    expect(await screen.findByLabelText('Password')).toBeTruthy()
    expect(screen.queryByRole('navigation')).toBeNull()
  })

  it('opens the app straight away when the session is valid', async () => {
    server({ loggedIn: true })
    render(App)
    expect(await screen.findByRole('navigation')).toBeTruthy()
    expect(screen.queryByLabelText('Password')).toBeNull()
  })

  it('signs in from the login screen and shows the app', async () => {
    server()
    render(App)
    await fireEvent.input(await screen.findByLabelText('Password'), { target: { value: 'pw' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('navigation')).toBeTruthy()
  })

  it('stays on the login screen when the password is refused', async () => {
    server({ loginAnswer: json({ error: 'invalid username or password' }, 401) })
    render(App)
    await fireEvent.input(await screen.findByLabelText('Password'), { target: { value: 'nope' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect((await screen.findByRole('alert')).textContent).toContain('invalid username or password')
    expect(screen.queryByRole('navigation')).toBeNull()
  })

  it('keeps the tab that was asked for across the login', async () => {
    server()
    window.history.replaceState({}, '', '/metrics')
    render(App)
    await fireEvent.input(await screen.findByLabelText('Password'), { target: { value: 'pw' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await screen.findByRole('navigation')
    const current = screen.getAllByRole('link').filter((a) => a.getAttribute('aria-current') === 'page')
    expect(current.map((a) => a.textContent.trim())).toEqual(['Metrics'])
  })

  it('signs out from the header and goes back to the login screen', async () => {
    const state = server({ loggedIn: true })
    render(App)
    await fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }))
    expect(await screen.findByLabelText('Password')).toBeTruthy()
    expect(screen.queryByRole('navigation')).toBeNull()
    expect(state.calls).toContainEqual(['/api/logout', 'POST'])
  })

  it('goes to the login screen when a request finds the session gone', async () => {
    const state = server({ loggedIn: true })
    render(App)
    await screen.findByRole('navigation')
    state.loggedIn = false // the session ended on the server
    await fireEvent.click(screen.getByRole('link', { name: 'Config' }))
    expect(await screen.findByLabelText('Password')).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('navigation')).toBeNull())
  })

  it('goes to the login screen when the log stream finds the session gone', async () => {
    const state = server({ loggedIn: true })
    // An EventSource that the server refused: the browser gives up at once, and only the server's answer says why.
    vi.stubGlobal(
      'EventSource',
      class extends EventTarget {
        static CONNECTING = 0
        static OPEN = 1
        static CLOSED = 2
        readyState = 2
        constructor() {
          super()
          queueMicrotask(() => this.dispatchEvent(new Event('error')))
        }
        close() {}
      },
    )
    state.streamRefused = true
    window.history.replaceState({}, '', '/logs')
    render(App)
    expect(await screen.findByLabelText('Password')).toBeTruthy()
  })

  it('says it cannot reach caddy-view when the check fails, and offers to try again', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    render(App)
    expect((await screen.findByRole('alert')).textContent).toContain('cannot reach caddy-view')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy()
  })
})
