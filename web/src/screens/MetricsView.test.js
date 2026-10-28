import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatClock, formatStamp } from '../lib/format.js'
import MetricsView from './MetricsView.svelte'

const NOW = new Date('2026-10-28T12:00:00Z')

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
const ok = (body) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

const bucket = (start, requests, bytes, status, avgMs, maxMs) => ({
  start,
  requests,
  bytes,
  status: { '1xx': 0, '2xx': 0, '3xx': 0, '4xx': 0, '5xx': 0, other: 0, ...status },
  avgMs,
  p95Ms: avgMs,
  maxMs,
})

const trafficAnswer = (resolution = 'hour', buckets) => ({
  resolution,
  bucketSeconds: resolution === 'hour' ? 3600 : 60,
  buckets: buckets ?? [
    bucket('2026-10-28T10:00:00Z', 100, 10000, { '2xx': 90, '4xx': 9, '5xx': 1 }, 20, 800),
    bucket('2026-10-28T11:00:00Z', 300, 30000, { '2xx': 297, '5xx': 3 }, 40, 3000),
  ],
})

const host = (name, requests, errors5xx = 0) => ({
  host: name,
  requests,
  bytes: requests * 100,
  errors5xx,
  status: {},
  avgMs: 20,
  p95Ms: 50,
  maxMs: 100,
})
const hostsAnswer = (list = [host('app.example.com', 300), host('api.example.com', 100, 4)]) => ({ hosts: list })

// api builds the fake server; `routes` overrides what /api/traffic and /api/hosts answer, as functions of the parameters.
function api(routes = {}) {
  const calls = { traffic: [], hosts: [] }
  vi.stubGlobal(
    'fetch',
    vi.fn((url) => {
      const u = new URL(url, 'http://x')
      const params = Object.fromEntries(u.searchParams)
      if (u.pathname === '/api/traffic') {
        calls.traffic.push(params)
        return (routes.traffic ?? ((p) => json(trafficAnswer(p.resolution))))(params)
      }
      if (u.pathname === '/api/hosts') {
        calls.hosts.push(params)
        return (routes.hosts ?? (() => json(hostsAnswer())))(params)
      }
      return Promise.reject(new Error(`unexpected request ${url}`))
    }),
  )
  return calls
}

const open = async (routes) => {
  const calls = api(routes)
  const view = render(MetricsView, { now: () => NOW })
  await screen.findByRole('group', { name: 'Requests' })
  return { ...view, calls }
}

const tile = (name) => screen.getByRole('group', { name })
const choosePeriod = (label) => fireEvent.click(screen.getByRole('radio', { name: label }))

afterEach(() => vi.unstubAllGlobals())

describe('MetricsView loading', () => {
  it('has a title and a one-line subtitle', async () => {
    await open()
    expect(screen.getByRole('heading', { level: 1, name: 'Metrics' })).toBeTruthy()
    expect(screen.getByText(/requests, status codes and response times/i)).toBeTruthy()
  })

  it('says it is loading before the first answer', () => {
    api({ traffic: () => new Promise(() => {}), hosts: () => new Promise(() => {}) })
    render(MetricsView, { now: () => NOW })
    expect(screen.getByText('Loading…')).toBeTruthy()
  })

  it('reads the last 24 hours by the hour, and the hosts of the same range', async () => {
    const { calls } = await open()
    const range = { resolution: 'hour', from: '2026-10-27T12:00:00Z', to: '2026-10-28T12:00:00Z' }
    expect(calls.traffic).toHaveLength(1)
    expect(calls.traffic[0]).toEqual(range)
    expect(calls.hosts).toHaveLength(1)
    expect(calls.hosts[0]).toMatchObject(range)
  })

  it('offers the four periods, with 24 hours chosen', async () => {
    await open()
    const group = screen.getByRole('radiogroup', { name: 'Period' })
    expect(within(group).getAllByRole('radio').map((r) => [r.textContent.trim(), r.getAttribute('aria-checked')])).toEqual([
      ['1 hour', 'false'],
      ['24 hours', 'true'],
      ['7 days', 'false'],
      ['30 days', 'false'],
    ])
  })
})

describe('MetricsView content', () => {
  it('totals the period in tiles', async () => {
    await open()
    expect(within(tile('Requests')).getByText('400')).toBeTruthy()
    expect(within(tile('Traffic')).getByText('39.1 KB')).toBeTruthy()
    expect(within(tile('Server errors')).getByText('1 %')).toBeTruthy()
    expect(within(tile('Server errors')).getByText('4 requests')).toBeTruthy()
    expect(within(tile('Avg. response time')).getByText('35 ms')).toBeTruthy()
    expect(within(tile('Avg. response time')).getByText('max 3 s')).toBeTruthy()
  })

  it('marks the tile of server errors', async () => {
    await open()
    expect(tile('Server errors').dataset.tone).toBe('bad')
  })

  it('draws the requests of each hour', async () => {
    const { container } = await open()
    expect(screen.getByRole('heading', { name: 'Requests per hour' })).toBeTruthy()
    expect(container.querySelectorAll('rect[data-bar]')).toHaveLength(2)
  })

  it('shows the share of each status class', async () => {
    await open()
    const table = screen.getByRole('table', { name: 'Status codes' })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows.map((r) => r.querySelector('td').textContent.trim())).toEqual(['2xx', '4xx', '5xx'])
    expect(table.textContent).toContain('387')
  })

  it('lists the hosts', async () => {
    await open()
    const table = screen.getByRole('table', { name: 'Hosts' })
    expect(within(table).getByText('app.example.com')).toBeTruthy()
    expect(within(table).getByText('api.example.com')).toBeTruthy()
  })

  it('says there was no traffic, with dashes where there is nothing to average', async () => {
    const empty = trafficAnswer('hour', [bucket('2026-10-28T10:00:00Z', 0, 0, {}, null, null)])
    await open({ traffic: () => json(empty), hosts: () => json(hostsAnswer([])) })
    expect(within(tile('Requests')).getByText('0')).toBeTruthy()
    expect(within(tile('Avg. response time')).getByText('–')).toBeTruthy()
    expect(within(tile('Server errors')).getByText('–')).toBeTruthy()
    expect(screen.getAllByText('No traffic in this period').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('No requests')).toBeTruthy()
  })

  it('renders hostile host names as text', async () => {
    const { container } = await open({ hosts: () => json(hostsAnswer([host('<img src=x onerror=alert(1)>', 5)])) })
    expect(container.querySelector('img')).toBeNull()
    expect(within(screen.getByRole('table', { name: 'Hosts' })).getByText('<img src=x onerror=alert(1)>')).toBeTruthy()
  })
})

describe('MetricsView periods', () => {
  it('reads the last hour by the minute', async () => {
    const { calls } = await open()
    await choosePeriod('1 hour')
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    expect(calls.traffic[1]).toEqual({ resolution: 'minute', from: '2026-10-28T11:00:00Z', to: '2026-10-28T12:00:00Z' })
    expect(calls.hosts[1]).toMatchObject({ resolution: 'minute', from: '2026-10-28T11:00:00Z' })
    expect(await screen.findByRole('heading', { name: 'Requests per minute' })).toBeTruthy()
  })

  it.each([
    ['7 days', '2026-10-21T12:00:00Z'],
    ['30 days', '2026-09-28T12:00:00Z'],
  ])('reads %s by the hour', async (label, from) => {
    const { calls } = await open()
    await choosePeriod(label)
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    expect(calls.traffic[1]).toEqual({ resolution: 'hour', from, to: '2026-10-28T12:00:00Z' })
  })

  it('marks the period that is shown', async () => {
    await open()
    await choosePeriod('7 days')
    expect(screen.getByRole('radio', { name: '7 days' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('radio', { name: '24 hours' }).getAttribute('aria-checked')).toBe('false')
  })

  it('labels a day with the time, and several days with their dates', async () => {
    const first = '2026-10-28T10:00:00Z'
    const { container } = await open()
    const table = () => container.querySelector('details tbody td').textContent
    expect(table()).toBe(formatClock(first))
    await choosePeriod('7 days')
    await waitFor(() => expect(table()).toBe(formatStamp(first)))
  })

  it('only the answer to the period that is shown counts', async () => {
    const waiting = []
    const { calls } = await open()
    vi.mocked(fetch).mockImplementation((url) => {
      const u = new URL(url, 'http://x')
      const params = Object.fromEntries(u.searchParams)
      if (u.pathname === '/api/traffic') return new Promise((resolve) => waiting.push({ params, resolve }))
      return json(hostsAnswer())
    })
    await choosePeriod('7 days')
    await waitFor(() => expect(waiting).toHaveLength(1))
    await choosePeriod('30 days')
    await waitFor(() => expect(waiting).toHaveLength(2))

    waiting[1].resolve(ok(trafficAnswer('hour', [bucket('2026-10-28T10:00:00Z', 7, 700, { '2xx': 7 }, 5, 9)])))
    await waitFor(() => expect(within(tile('Requests')).getByText('7')).toBeTruthy())
    waiting[0].resolve(ok(trafficAnswer('hour', [bucket('2026-10-28T10:00:00Z', 999, 1, { '2xx': 999 }, 5, 9)])))
    await new Promise((r) => setTimeout(r, 10))
    expect(within(tile('Requests')).getByText('7')).toBeTruthy()
    expect(calls.traffic.length).toBe(1) // the calls after the first went to the new mock
  })
})

describe('MetricsView host filter', () => {
  it('has a select with every host the period has', async () => {
    await open()
    const select = screen.getByLabelText('Host')
    expect(Array.from(select.options).map((o) => [o.value, o.textContent.trim()])).toEqual([
      ['', 'All hosts'],
      ['app.example.com', 'app.example.com'],
      ['api.example.com', 'api.example.com'],
    ])
  })

  it('reads the traffic of the host that is chosen, and leaves the hosts table as it is', async () => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'api.example.com' } })
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    expect(calls.traffic[1]).toMatchObject({ host: 'api.example.com', resolution: 'hour' })
    expect(calls.hosts).toHaveLength(1)
    expect(within(screen.getByRole('table', { name: 'Hosts' })).getByText('app.example.com')).toBeTruthy()
  })

  it('takes the host whose name is clicked in the table, and drops it when it is clicked again', async () => {
    const { calls } = await open()
    await fireEvent.click(screen.getByRole('button', { name: 'api.example.com' }))
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    expect(calls.traffic[1].host).toBe('api.example.com')
    expect(screen.getByLabelText('Host').value).toBe('api.example.com')
    expect(screen.getByRole('button', { name: 'api.example.com' }).getAttribute('aria-pressed')).toBe('true')

    await fireEvent.click(screen.getByRole('button', { name: 'api.example.com' }))
    await waitFor(() => expect(calls.traffic).toHaveLength(3))
    expect(calls.traffic[2].host).toBeUndefined()
    expect(screen.getByLabelText('Host').value).toBe('')
  })

  it('keeps the host when the period changes', async () => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'app.example.com' } })
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    await choosePeriod('7 days')
    await waitFor(() => expect(calls.traffic).toHaveLength(3))
    expect(calls.traffic[2]).toMatchObject({ host: 'app.example.com', from: '2026-10-21T12:00:00Z' })
  })

  it('keeps a host that the new period no longer has', async () => {
    let call = 0
    const { calls } = await open({ hosts: () => json(++call === 1 ? hostsAnswer() : hostsAnswer([host('other.example.com', 1)])) })
    await fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'app.example.com' } })
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    await choosePeriod('1 hour')
    await waitFor(() => expect(calls.hosts).toHaveLength(2))
    const select = screen.getByLabelText('Host')
    await waitFor(() => expect(Array.from(select.options).map((o) => o.value)).toContain('other.example.com'))
    expect(select.value).toBe('app.example.com')
    expect(Array.from(select.options).map((o) => o.value)).toContain('app.example.com')
  })
})

describe('MetricsView refreshing and failing', () => {
  it('reads everything again when asked', async () => {
    const { calls } = await open()
    await fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls.traffic).toHaveLength(2))
    expect(calls.hosts).toHaveLength(2)
  })

  it('says the history is off when its routes are not there, and it is not an error', async () => {
    const gone = () => json({ error: 'not found' }, 404)
    api({ traffic: gone, hosts: gone })
    render(MetricsView, { now: () => NOW })
    expect(await screen.findByText(/traffic history is off/i)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('group', { name: 'Requests' })).toBeNull()
  })

  it('shows an error when the traffic cannot be read', async () => {
    api({ traffic: () => json({ error: 'internal error' }, 500) })
    render(MetricsView, { now: () => NOW })
    expect((await screen.findByRole('alert')).textContent).toContain('internal error')
  })

  it('shows an error when caddy-view cannot be reached', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    render(MetricsView, { now: () => NOW })
    expect((await screen.findByRole('alert')).textContent).toMatch(/caddy-view/)
  })

  it('still shows the tiles and the chart when only the hosts cannot be read', async () => {
    await open({ hosts: () => json({ error: 'internal error' }, 500) })
    expect(within(tile('Requests')).getByText('400')).toBeTruthy()
    expect(screen.queryByRole('table', { name: 'Hosts' })).toBeNull()
    expect(screen.getByLabelText('Host').options).toHaveLength(1)
  })
})
