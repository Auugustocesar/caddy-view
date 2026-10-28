import { describe, expect, it } from 'vitest'
import { PERIODS, chartTitle, periodById, periodRange, statusLabel, statusShares, summarize } from './metrics.js'

const bucket = (requests, extra = {}) => ({
  start: '2026-10-28T10:00:00Z',
  requests,
  bytes: requests * 100,
  status: { '1xx': 0, '2xx': requests, '3xx': 0, '4xx': 0, '5xx': 0, other: 0 },
  avgMs: requests ? 10 : null,
  p95Ms: requests ? 25 : null,
  maxMs: requests ? 30 : null,
  ...extra,
})

describe('PERIODS', () => {
  it('are an hour by minutes and then days by hours', () => {
    expect(PERIODS.map((p) => [p.id, p.resolution, p.hours])).toEqual([
      ['1h', 'minute', 1],
      ['24h', 'hour', 24],
      ['7d', 'hour', 168],
      ['30d', 'hour', 720],
    ])
  })

  it('never ask for more buckets than the API allows', () => {
    for (const p of PERIODS) {
      const buckets = p.resolution === 'minute' ? p.hours * 60 : p.hours
      expect(buckets, p.id).toBeLessThanOrEqual(3000)
    }
  })

  it('have a label that says what they are', () => {
    expect(PERIODS.map((p) => p.label)).toEqual(['1 hour', '24 hours', '7 days', '30 days'])
  })
})

describe('periodById', () => {
  it.each(['1h', '24h', '7d', '30d'])('finds %s', (id) => {
    expect(periodById(id).id).toBe(id)
  })

  it('falls back to 24 hours for an id it does not know', () => {
    expect(periodById('forever').id).toBe('24h')
    expect(periodById(undefined).id).toBe('24h')
  })
})

describe('periodRange', () => {
  const now = new Date('2026-10-28T12:34:56.789Z')

  it('ends now and starts a period before, in RFC 3339 with whole seconds', () => {
    expect(periodRange(periodById('24h'), now)).toEqual({ resolution: 'hour', from: '2026-10-27T12:34:56Z', to: '2026-10-28T12:34:56Z' })
    expect(periodRange(periodById('1h'), now)).toEqual({ resolution: 'minute', from: '2026-10-28T11:34:56Z', to: '2026-10-28T12:34:56Z' })
  })

  it('goes back days for the long periods', () => {
    expect(periodRange(periodById('7d'), now).from).toBe('2026-10-21T12:34:56Z')
    expect(periodRange(periodById('30d'), now).from).toBe('2026-09-28T12:34:56Z')
  })
})

describe('chartTitle', () => {
  it('names the bucket', () => {
    expect(chartTitle('minute')).toBe('Requests per minute')
    expect(chartTitle('hour')).toBe('Requests per hour')
  })
})

describe('summarize', () => {
  it('adds up requests and bytes', () => {
    const got = summarize([bucket(10), bucket(5), bucket(0)])
    expect(got.requests).toBe(15)
    expect(got.bytes).toBe(1500)
  })

  it('counts the server errors and their share of the requests', () => {
    const got = summarize([bucket(8, { status: { '2xx': 6, '4xx': 1, '5xx': 1 } }), bucket(2, { status: { '2xx': 1, '5xx': 1 } })])
    expect(got.errors5xx).toBe(2)
    expect(got.errorRate).toBeCloseTo(0.2, 10)
  })

  it('weights the average response time by the requests of each bucket', () => {
    const got = summarize([bucket(10, { avgMs: 10 }), bucket(30, { avgMs: 50 }), bucket(0)])
    expect(got.avgMs).toBeCloseTo((10 * 10 + 30 * 50) / 40, 10)
  })

  it('takes the slowest request of all as the maximum', () => {
    expect(summarize([bucket(1, { maxMs: 30 }), bucket(1, { maxMs: 3000 }), bucket(0)]).maxMs).toBe(3000)
  })

  it('sums every status class', () => {
    const got = summarize([bucket(3, { status: { '2xx': 2, '4xx': 1 } }), bucket(2, { status: { '2xx': 1, '3xx': 1 } })])
    expect(got.status).toEqual({ '1xx': 0, '2xx': 3, '3xx': 1, '4xx': 1, '5xx': 0, other: 0 })
  })

  it('has nothing to average or divide by without requests', () => {
    for (const buckets of [[], null, undefined, [bucket(0), bucket(0)]]) {
      expect(summarize(buckets)).toEqual({
        requests: 0,
        bytes: 0,
        errors5xx: 0,
        errorRate: null,
        avgMs: null,
        maxMs: null,
        status: { '1xx': 0, '2xx': 0, '3xx': 0, '4xx': 0, '5xx': 0, other: 0 },
      })
    }
  })

  it('does not break on a bucket without a status', () => {
    expect(summarize([{ requests: 2, bytes: 10, avgMs: 4, maxMs: 6 }]).requests).toBe(2)
  })
})

describe('statusLabel', () => {
  it.each([
    ['1xx', 'Informational'],
    ['2xx', 'Success'],
    ['3xx', 'Redirect'],
    ['4xx', 'Client error'],
    ['5xx', 'Server error'],
    ['other', 'Other'],
    ['weird', 'weird'],
  ])('%s is %s', (key, label) => expect(statusLabel(key)).toBe(label))
})

describe('statusShares', () => {
  it('lists the classes that have requests, in order, with their share', () => {
    expect(statusShares({ '5xx': 1, '2xx': 90, '4xx': 9, '3xx': 0, '1xx': 0, other: 0 })).toEqual([
      { key: '2xx', label: 'Success', count: 90, share: 0.9 },
      { key: '4xx', label: 'Client error', count: 9, share: 0.09 },
      { key: '5xx', label: 'Server error', count: 1, share: 0.01 },
    ])
  })

  it('keeps the informational and other classes when they have requests', () => {
    expect(statusShares({ '1xx': 1, '2xx': 1, other: 2 }).map((s) => s.key)).toEqual(['1xx', '2xx', 'other'])
  })

  it('shares add up to one', () => {
    const total = statusShares({ '2xx': 7, '3xx': 2, '4xx': 1 }).reduce((sum, s) => sum + s.share, 0)
    expect(total).toBeCloseTo(1, 10)
  })

  it.each([[{}], [null], [undefined], [{ '2xx': 0, '5xx': 0 }]])('is empty for %j', (status) => {
    expect(statusShares(status)).toEqual([])
  })
})
