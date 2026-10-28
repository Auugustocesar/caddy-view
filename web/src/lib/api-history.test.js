import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, getHosts, getLogs, getTraffic } from './api.js'

function respond(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const fetchOf = (body = {}, status = 200) => {
  const fetchMock = vi.fn().mockResolvedValue(respond(status, body))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const urlOf = (fetchMock) => fetchMock.mock.calls[0][0]
const params = (fetchMock) => Object.fromEntries(new URL(urlOf(fetchMock), 'http://x').searchParams)
const pathOf = (fetchMock) => new URL(urlOf(fetchMock), 'http://x').pathname

afterEach(() => vi.unstubAllGlobals())

describe('getTraffic with a range', () => {
  it('asks for the resolution alone when there is nothing else', async () => {
    const fetchMock = fetchOf({ buckets: [] })
    await getTraffic('hour')
    expect(urlOf(fetchMock)).toBe('/api/traffic?resolution=hour')
  })

  it('sends the range and the host it is given', async () => {
    const fetchMock = fetchOf({ buckets: [] })
    await getTraffic('minute', { from: '2026-10-28T10:00:00Z', to: '2026-10-28T11:00:00Z', host: 'app.example.com' })
    expect(pathOf(fetchMock)).toBe('/api/traffic')
    expect(params(fetchMock)).toEqual({ resolution: 'minute', from: '2026-10-28T10:00:00Z', to: '2026-10-28T11:00:00Z', host: 'app.example.com' })
  })

  it('leaves out what is empty', async () => {
    const fetchMock = fetchOf({ buckets: [] })
    await getTraffic('hour', { from: '', host: '', to: undefined })
    expect(urlOf(fetchMock)).toBe('/api/traffic?resolution=hour')
  })

  it('encodes a host that needs it', async () => {
    const fetchMock = fetchOf({ buckets: [] })
    await getTraffic('hour', { host: 'a&b=c d' })
    expect(params(fetchMock).host).toBe('a&b=c d')
  })
})

describe('getHosts', () => {
  it('reads the totals per host', async () => {
    const fetchMock = fetchOf({ hosts: [{ host: 'a', requests: 1 }] })
    await expect(getHosts('hour')).resolves.toEqual({ hosts: [{ host: 'a', requests: 1 }] })
    expect(urlOf(fetchMock)).toBe('/api/hosts?resolution=hour')
  })

  it('sends the range and the limit', async () => {
    const fetchMock = fetchOf({ hosts: [] })
    await getHosts('minute', { from: '2026-10-28T10:00:00Z', to: '2026-10-28T11:00:00Z', limit: 20 })
    expect(pathOf(fetchMock)).toBe('/api/hosts')
    expect(params(fetchMock)).toEqual({ resolution: 'minute', from: '2026-10-28T10:00:00Z', to: '2026-10-28T11:00:00Z', limit: '20' })
  })

  it('answers 404 when the history is off, so the screen can tell', async () => {
    fetchOf({ error: 'not found' }, 404)
    const err = await getHosts('hour').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(404)
  })
})

describe('getLogs', () => {
  it('asks for the newest entries with no filter', async () => {
    const fetchMock = fetchOf({ entries: [], next: null })
    await expect(getLogs()).resolves.toEqual({ entries: [], next: null })
    expect(urlOf(fetchMock)).toBe('/api/logs')
  })

  it('sends every filter it is given', async () => {
    const fetchMock = fetchOf({ entries: [], next: null })
    await getLogs({ host: 'app.example.com', minStatus: 500, maxStatus: 599, q: '/api/', from: '2026-10-28T10:00:00Z', to: '2026-10-28T11:00:00Z', limit: 50, before: 812 })
    expect(pathOf(fetchMock)).toBe('/api/logs')
    expect(params(fetchMock)).toEqual({
      host: 'app.example.com',
      minStatus: '500',
      maxStatus: '599',
      q: '/api/',
      from: '2026-10-28T10:00:00Z',
      to: '2026-10-28T11:00:00Z',
      limit: '50',
      before: '812',
    })
  })

  it('leaves out what is empty, and keeps a zero', async () => {
    const fetchMock = fetchOf({ entries: [], next: null })
    await getLogs({ host: '', q: '', minStatus: undefined, before: 0 })
    expect(params(fetchMock)).toEqual({ before: '0' })
  })

  it('encodes the text it searches for', async () => {
    const fetchMock = fetchOf({ entries: [], next: null })
    await getLogs({ q: '/search?x=1&y=2#z' })
    expect(params(fetchMock).q).toBe('/search?x=1&y=2#z')
  })

  it('answers 404 when the history is off', async () => {
    fetchOf({ error: 'not found' }, 404)
    expect((await getLogs().catch((e) => e)).status).toBe(404)
  })
})
