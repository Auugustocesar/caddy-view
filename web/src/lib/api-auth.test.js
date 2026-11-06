import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, getSession, getStatus, login, logout, setUnauthorizedHandler } from './api.js'

function respond(status, body, headers = {}) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  setUnauthorizedHandler(null)
})

describe('getSession', () => {
  it('says who is logged in', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, { user: { username: 'admin' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getSession()).resolves.toEqual({ required: true, user: 'admin' })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/me')
  })

  it('says a login is needed on 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(401, { error: 'login required' })))
    await expect(getSession()).resolves.toEqual({ required: true, user: null })
  })

  it('says there is no login when the route is not there', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(404, { error: 'not found' })))
    await expect(getSession()).resolves.toEqual({ required: false, user: null })
  })

  it('does not hide other failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(500, { error: 'could not check the session' })))
    await expect(getSession()).rejects.toMatchObject({ status: 500 })
  })

  it('does not call the unauthorized handler: a 401 here is the answer', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(401, { error: 'login required' })))
    await getSession()
    expect(handler).not.toHaveBeenCalled()
  })
})

describe('login', () => {
  it('posts the credentials as JSON and returns the user', async () => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, { user: { username: 'admin' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(login('admin', 'pw')).resolves.toBe('admin')
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/api/login')
    expect(init.method).toBe('POST')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual({ username: 'admin', password: 'pw' })
  })

  it('throws the server message on a refused login, without calling the unauthorized handler', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(401, { error: 'invalid username or password' })))
    const err = await login('admin', 'nope').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 401, message: 'invalid username or password' })
    expect(handler).not.toHaveBeenCalled()
  })

  it('tells how long to wait when held back', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(429, { error: 'too many failed logins' }, { 'Retry-After': '90' })))
    const err = await login('admin', 'nope').catch((e) => e)
    expect(err).toMatchObject({ status: 429, retryAfter: 90 })
  })

  it('ignores a Retry-After that is not a whole number of seconds', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(429, { error: 'x' }, { 'Retry-After': 'soon' })))
    const err = await login('admin', 'nope').catch((e) => e)
    expect(err.retryAfter).toBeUndefined()
  })
})

describe('logout', () => {
  it('posts to /api/logout as JSON and accepts the empty answer', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(logout()).resolves.toBeUndefined()
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/api/logout')
    expect(init.method).toBe('POST')
    expect(init.headers['Content-Type']).toBe('application/json')
  })
})

describe('the unauthorized handler', () => {
  it('is called when any other route answers 401, and the error still reaches the caller', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(401, { error: 'login required' })))
    await expect(getStatus()).rejects.toMatchObject({ status: 401 })
    expect(handler).toHaveBeenCalledOnce()
  })

  it('is not called for other failures', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(502, { error: 'bad gateway' })))
    await expect(getStatus()).rejects.toMatchObject({ status: 502 })
    expect(handler).not.toHaveBeenCalled()
  })
})
