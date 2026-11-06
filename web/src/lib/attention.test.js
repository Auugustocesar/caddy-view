import { describe, expect, it } from 'vitest'
import { compare, describeItems } from './attention.js'

describe('compare', () => {
  it('says how much a count moved against the previous period', () => {
    expect(compare(1060, 1000)).toEqual({ text: '+6% on the previous period', tone: undefined })
    expect(compare(900, 1000)).toEqual({ text: '−10% on the previous period', tone: undefined })
  })

  it('says nothing without a previous period or with nothing to compare', () => {
    expect(compare(10, null)).toBeNull()
    expect(compare(10, 0)).toBeNull()
    expect(compare(null, 10)).toBeNull()
  })

  it('says it is the same when it did not move', () => {
    expect(compare(1000, 1000).text).toBe('the same as the previous period')
  })

  it('in times for a rate, and warns when a worse thing grew a lot', () => {
    expect(compare(0.0021, 0.001, { times: true, worseWhenHigher: true })).toEqual({ text: '2.1x the previous period', tone: 'bad' })
  })

  it('does not warn for a rate that fell', () => {
    expect(compare(0.001, 0.002, { times: true, worseWhenHigher: true }).tone).toBeUndefined()
  })

  it('does not warn for a small rise', () => {
    expect(compare(0.0012, 0.001, { times: true, worseWhenHigher: true }).tone).toBeUndefined()
  })
})

describe('describeItems', () => {
  const down = (host, addrs, total) => ({ kind: 'upstream_down', severity: addrs.length === total ? 'bad' : 'warn', host, down: addrs, total })

  it('says a site with its only upstream down is down', () => {
    const [item] = describeItems([down('admin.example.com', ['10.0.0.9:80'], 1)])
    expect(item.severity).toBe('bad')
    expect(item.title).toBe('admin.example.com is down')
    expect(item.text).toContain('10.0.0.9:80')
    expect(item.text).toContain('Visitors get an error')
  })

  it('says a site with some upstreams down is degraded, with the numbers', () => {
    const [item] = describeItems([down('reports.example.com', ['10.0.0.9:80'], 2)])
    expect(item.severity).toBe('warn')
    expect(item.title).toBe('reports.example.com is degraded')
    expect(item.text).toContain('1 of 2')
  })

  it('groups the down sites into one item when there are many', () => {
    const items = describeItems(['a', 'b', 'c', 'd'].map((h) => down(`${h}.example.com`, [`${h}:1`], 1)))
    expect(items).toHaveLength(1)
    expect(items[0].title).toBe('4 sites are down')
    expect(items[0].text).toContain('a.example.com')
    expect(items[0].text).toContain('d.example.com')
  })

  it('does not group three or fewer', () => {
    const items = describeItems(['a', 'b', 'c'].map((h) => down(`${h}.example.com`, [`${h}:1`], 1)))
    expect(items).toHaveLength(3)
  })

  it('says errors went up with both rates', () => {
    const [item] = describeItems([{ kind: 'errors_up', severity: 'bad', host: 'api.example.com', errors5xx: 9, rate: 0.009, previousRate: 0.003 }])
    expect(item.title).toBe('api.example.com returns more errors')
    expect(item.text).toContain('0.9 %')
    expect(item.text).toContain('0.3 %')
  })

  it('says errors appeared when the previous period had none', () => {
    const [item] = describeItems([{ kind: 'errors_up', severity: 'bad', host: 'api.example.com', errors5xx: 9, rate: 0.009 }])
    expect(item.text).toContain('none in the previous period')
  })

  it('says a host is slow, against the median', () => {
    const [item] = describeItems([{ kind: 'host_slow', severity: 'warn', host: 'reports.example.com', p95Ms: 640, medianP95Ms: 71 }])
    expect(item.title).toBe('reports.example.com is slow')
    expect(item.text).toContain('640 ms')
    expect(item.text).toContain('9 times')
  })

  it('explains that Caddy has no health checks', () => {
    const [item] = describeItems([{ kind: 'no_health_checks', severity: 'info' }])
    expect(item.title).toBe('Caddy has no health checks')
    expect(item.severity).toBe('info')
  })

  it('names the catch-all site', () => {
    const [item] = describeItems([down('', ['x:1'], 1)])
    expect(item.title).toBe('(any host) is down')
  })

  it('keeps the order of the items it is given', () => {
    const items = describeItems([
      { kind: 'errors_up', severity: 'bad', host: 'e.example.com', errors5xx: 9, rate: 0.1 },
      { kind: 'host_slow', severity: 'warn', host: 's.example.com', p95Ms: 300, medianP95Ms: 50 },
    ])
    expect(items.map((i) => i.severity)).toEqual(['bad', 'warn'])
  })

  it('is empty for no items', () => {
    expect(describeItems([])).toEqual([])
    expect(describeItems(undefined)).toEqual([])
  })
})
