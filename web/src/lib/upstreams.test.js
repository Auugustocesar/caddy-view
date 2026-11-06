import { describe, expect, it } from 'vitest'
import { flattenUpstreams, sortFailingFirst, summarizeUpstreams, visibleUpstreams } from './upstreams.js'

const sites = {
  upstreamsAvailable: true,
  upstreamCheck: true,
  sites: [
    { server: 'srv0', host: 'app.example.com', upstreams: [{ address: '10.0.0.5:8080', numRequests: 1, fails: 0, state: 'answering', latencyMs: 12.4, httpStatus: 200 }] },
    {
      server: 'srv0',
      host: 'api.example.com',
      upstreams: [
        { address: '10.0.0.6:9000', numRequests: 0, fails: 0, state: 'down' },
        { address: '10.0.0.7:9000', numRequests: 0, fails: 0, state: 'answering', latencyMs: 3, httpStatus: 404 },
      ],
    },
    { server: 'srv0', host: 'files.example.com', upstreams: [] },
    { server: 'srv0', host: 'far.example.com', upstreams: [{ address: '172.16.0.1:80', numRequests: 0, fails: 0 }] },
  ],
}

describe('flattenUpstreams', () => {
  it('makes one row per upstream, with the host it belongs to', () => {
    const rows = flattenUpstreams(sites)
    expect(rows.map((r) => [r.host, r.address])).toEqual([
      ['app.example.com', '10.0.0.5:8080'],
      ['api.example.com', '10.0.0.6:9000'],
      ['api.example.com', '10.0.0.7:9000'],
      ['far.example.com', '172.16.0.1:80'],
    ])
  })

  it('keeps the state, latency and status of the check', () => {
    const [app] = flattenUpstreams(sites)
    expect(app).toMatchObject({ state: 'answering', latencyMs: 12.4, httpStatus: 200 })
  })

  it('calls an upstream without a state unknown', () => {
    const far = flattenUpstreams(sites).find((r) => r.host === 'far.example.com')
    expect(far.state).toBe('unknown')
  })

  it('gives the number of upstreams of its site, so a site with two can say 1 of 2', () => {
    const rows = flattenUpstreams(sites)
    expect(rows.find((r) => r.host === 'api.example.com').siblings).toBe(2)
    expect(rows.find((r) => r.host === 'app.example.com').siblings).toBe(1)
  })

  it('has no rows without sites', () => {
    expect(flattenUpstreams(null)).toEqual([])
    expect(flattenUpstreams({ sites: [] })).toEqual([])
  })
})

describe('sortFailingFirst', () => {
  it('puts down first, then unknown, then answering, keeping the order inside each', () => {
    const rows = flattenUpstreams(sites)
    expect(sortFailingFirst(rows).map((r) => `${r.state}:${r.address}`)).toEqual([
      'down:10.0.0.6:9000',
      'unknown:172.16.0.1:80',
      'answering:10.0.0.5:8080',
      'answering:10.0.0.7:9000',
    ])
  })

  it('does not change the list it is given', () => {
    const rows = flattenUpstreams(sites)
    const before = rows.map((r) => r.address)
    sortFailingFirst(rows)
    expect(rows.map((r) => r.address)).toEqual(before)
  })
})

describe('summarizeUpstreams', () => {
  it('counts each state', () => {
    expect(summarizeUpstreams(flattenUpstreams(sites))).toEqual({ answering: 2, down: 1, unknown: 1, total: 4 })
  })
})

describe('visibleUpstreams', () => {
  const many = Array.from({ length: 20 }, (_, i) => ({ address: `h:${i}`, state: i < 2 ? 'down' : 'answering', host: `h${i}` }))

  it('shows every down upstream, then healthy ones up to the limit, and says how many are hidden', () => {
    const { rows, hidden } = visibleUpstreams(many, 5, false)
    expect(rows).toHaveLength(5)
    expect(rows.slice(0, 2).every((r) => r.state === 'down')).toBe(true)
    expect(hidden).toBe(15)
  })

  it('never hides a down upstream, even past the limit', () => {
    const allDown = many.map((r) => ({ ...r, state: 'down' }))
    const { rows, hidden } = visibleUpstreams(allDown, 5, false)
    expect(rows).toHaveLength(20)
    expect(hidden).toBe(0)
  })

  it('shows everything once expanded', () => {
    const { rows, hidden } = visibleUpstreams(many, 5, true)
    expect(rows).toHaveLength(20)
    expect(hidden).toBe(0)
  })

  it('has nothing hidden when the list is short', () => {
    expect(visibleUpstreams(many.slice(0, 3), 5, false)).toMatchObject({ hidden: 0 })
  })
})
