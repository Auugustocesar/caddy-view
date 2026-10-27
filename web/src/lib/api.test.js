import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, getConfig, getSites, getStatus } from './api.js'

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
