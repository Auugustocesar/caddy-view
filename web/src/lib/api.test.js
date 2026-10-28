import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  applyCaddyfile,
  formatCaddyfile,
  getCaddyfile,
  getConfig,
  getRevision,
  getRevisions,
  getSites,
  getStatus,
  getSummary,
  getSystem,
  getTraffic,
  rollbackRevision,
  validateCaddyfile,
} from './api.js'

function respond(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => vi.unstubAllGlobals())

describe('the API client', () => {
  it.each([
    ['getStatus', getStatus, '/api/status', { caddy: { reachable: true, endpoint: 'http://localhost:2019' } }],
    ['getSites', getSites, '/api/sites', { sites: [], upstreamsAvailable: true }],
    ['getConfig', getConfig, '/api/config', { apps: {} }],
  ])('%s reads %s', async (_name, call, path, body) => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, body))
    vi.stubGlobal('fetch', fetchMock)

    await expect(call()).resolves.toEqual(body)
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0][0]).toBe(path)
  })

  it.each([
    ['getSummary', () => getSummary('24h'), '/api/summary?window=24h', { window: '24h', requests: 5 }],
    ['getTraffic', () => getTraffic('hour'), '/api/traffic?resolution=hour', { resolution: 'hour', buckets: [] }],
  ])('%s reads %s', async (_name, call, path, body) => {
    const fetchMock = vi.fn().mockResolvedValue(respond(200, body))
    vi.stubGlobal('fetch', fetchMock)

    await expect(call()).resolves.toEqual(body)
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0][0]).toBe(path)
  })

  it('tells the history being off apart from other failures by the status', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(404, { error: 'not found', code: 'not-found' })))

    const err = await getSummary('24h').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(404)
  })

  it('uses the message of an error response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond(502, { error: 'caddy is down' })))

    const err = await getConfig().catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(502)
    expect(err.message).toBe('caddy is down')
  })

  it('falls back to the status when the error body is not the usual JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>oops</html>', { status: 500 })))

    const err = await getSites().catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(500)
    expect(err.message).toContain('500')
  })

  it('reports an unreachable server with status 0', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    const err = await getStatus().catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(0)
    expect(err.message).toMatch(/caddy-view/i)
  })

  it('rejects a success response that is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not json', { status: 200 })))

    await expect(getStatus()).rejects.toBeInstanceOf(ApiError)
  })
})

describe('the editing calls', () => {
  const fetchOf = (body = {}, status = 200) => {
    const fetchMock = vi.fn().mockResolvedValue(respond(status, body))
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  it.each([
    ['getCaddyfile', () => getCaddyfile(), '/api/caddyfile'],
    ['getSystem', () => getSystem(), '/api/system'],
    ['getRevisions', () => getRevisions(), '/api/revisions'],
    ['getRevisions with a page', () => getRevisions({ limit: 20, before: 5 }), '/api/revisions?limit=20&before=5'],
    ['getRevision', () => getRevision(3), '/api/revisions/3'],
  ])('%s reads %s', async (_name, call, path) => {
    const fetchMock = fetchOf({ ok: true })
    await call()
    expect(fetchMock.mock.calls[0][0]).toBe(path)
    expect(fetchMock.mock.calls[0][1]?.method ?? 'GET').toBe('GET')
  })

  it.each([
    ['validateCaddyfile', () => validateCaddyfile('a {\n}'), '/api/caddyfile/validate', { content: 'a {\n}' }],
    ['formatCaddyfile', () => formatCaddyfile('a{}'), '/api/caddyfile/format', { content: 'a{}' }],
    ['applyCaddyfile', () => applyCaddyfile('a', 'abc'), '/api/caddyfile/apply', { content: 'a', baseSha256: 'abc' }],
    ['rollbackRevision', () => rollbackRevision(3), '/api/revisions/3/rollback', {}],
  ])('%s posts JSON to %s', async (_name, call, path, body) => {
    const fetchMock = fetchOf({ ok: true })
    await call()
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe(path)
    expect(init.method).toBe('POST')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual(body)
  })

  it('returns what the server answered', async () => {
    fetchOf({ valid: false, error: 'bad', line: 3 })
    await expect(validateCaddyfile('x')).resolves.toEqual({ valid: false, error: 'bad', line: 3 })
  })

  it('keeps the code and the line of a refused change', async () => {
    fetchOf({ error: 'unrecognized directive: nope', code: 'invalid', line: 4 }, 422)
    const err = await applyCaddyfile('x', 'abc').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(422)
    expect(err.code).toBe('invalid')
    expect(err.line).toBe(4)
    expect(err.message).toBe('unrecognized directive: nope')
  })

  it('has no code or line for an error that came without them', async () => {
    fetchOf({ error: 'boom' }, 500)
    const err = await formatCaddyfile('x').catch((e) => e)
    expect(err.code).toBeUndefined()
    expect(err.line).toBeUndefined()
  })

  it('reports an unreachable server with status 0 on a post as on a get', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const err = await applyCaddyfile('x', 'abc').catch((e) => e)
    expect(err.status).toBe(0)
  })
})
