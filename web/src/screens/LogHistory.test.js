import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatBytes, formatMs, formatStamp } from '../lib/format.js'
import LogHistory from './LogHistory.svelte'

const NOW = new Date('2026-10-28T12:00:00Z')

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
const ok = (body) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

const entry = (id, extra = {}) => ({
  id,
  time: '2026-10-28T11:59:00Z',
  host: 'app.example.com',
  method: 'GET',
  path: '/index.html',
  status: 200,
  durationMs: 12.5,
  bytes: 2048,
  clientIp: '10.0.0.7',
  ...extra,
})

const entries = [
  entry(30),
  entry(29, { host: 'api.example.com', method: 'POST', path: '/api/orders', status: 502, durationMs: 3000, bytes: 0, clientIp: '10.0.0.9' }),
  entry(28, { path: '/missing', status: 404, durationMs: 2, bytes: 153 }),
]

const hostsAnswer = { hosts: [{ host: 'app.example.com', requests: 5 }, { host: 'api.example.com', requests: 2 }] }

// api builds the fake server; `routes` overrides what /api/logs and /api/hosts answer, as functions of the parameters.
function api(routes = {}) {
  const calls = { logs: [], hosts: [] }
  vi.stubGlobal(
    'fetch',
    vi.fn((url) => {
      const u = new URL(url, 'http://x')
      const params = Object.fromEntries(u.searchParams)
      if (u.pathname === '/api/logs') {
        calls.logs.push(params)
        return (routes.logs ?? (() => json({ entries, next: null })))(params)
      }
      if (u.pathname === '/api/hosts') {
        calls.hosts.push(params)
        return (routes.hosts ?? (() => json(hostsAnswer)))(params)
      }
      return Promise.reject(new Error(`unexpected request ${url}`))
    }),
  )
  return calls
}

const open = async (routes) => {
  const calls = api(routes)
  const view = render(LogHistory, { now: () => NOW, searchDelay: 0 })
  await screen.findByRole('table', { name: 'Requests' })
  return { ...view, calls }
}

const table = () => screen.getByRole('table', { name: 'Requests' })
const rows = () => within(table()).getAllByRole('row').slice(1) // the first row is the header
const cellsOf = (row) => Array.from(row.querySelectorAll('td')).map((c) => c.textContent.trim())

afterEach(() => vi.unstubAllGlobals())

describe('LogHistory loading', () => {
  it('asks for the newest requests of the last 24 hours, fifty at a time', async () => {
    const { calls } = await open()
    expect(calls.logs).toHaveLength(1)
    expect(calls.logs[0]).toEqual({ from: '2026-10-27T12:00:00Z', limit: '50' })
  })

  it('shows them in the order the API gave, which is newest first', async () => {
    await open()
    expect(rows().map((r) => r.querySelector('[data-path]')?.textContent ?? cellsOf(r)[4])).toEqual(['/index.html', '/api/orders', '/missing'])
  })

  it('shows what each request was', async () => {
    await open()
    expect(cellsOf(rows()[1])).toEqual([
      formatStamp('2026-10-28T11:59:00Z'),
      '502',
      'POST',
      'api.example.com',
      '/api/orders',
      formatMs(3000),
      formatBytes(0),
      '10.0.0.9',
    ])
    expect(cellsOf(rows()[0])[5]).toBe(formatMs(12.5))
    expect(cellsOf(rows()[0])[6]).toBe(formatBytes(2048))
  })

  it('marks the status class of each request so it can be coloured', async () => {
    await open()
    expect(rows().map((r) => r.querySelector('[data-status]').dataset.status)).toEqual(['2xx', '5xx', '4xx'])
  })

  it('says when no request matches', async () => {
    api({ logs: () => json({ entries: [], next: null }) })
    render(LogHistory, { now: () => NOW, searchDelay: 0 })
    expect(await screen.findByText('No requests match')).toBeTruthy()
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('renders hostile paths as text', async () => {
    const { container } = await open({ logs: () => json({ entries: [entry(1, { path: '<img src=x onerror=alert(1)>' })], next: null }) })
    expect(container.querySelector('img')).toBeNull()
    expect(within(table()).getByText('<img src=x onerror=alert(1)>')).toBeTruthy()
  })
})

describe('LogHistory filters', () => {
  it('has a search for the path, a host, a status class and a period', async () => {
    await open()
    expect(screen.getByLabelText('Search path').type).toBe('search')
    expect(Array.from(screen.getByLabelText('Host').options).map((o) => o.value)).toEqual(['', 'app.example.com', 'api.example.com'])
    expect(Array.from(screen.getByLabelText('Status').options).map((o) => o.textContent.trim())).toEqual(['Any status', '2xx', '3xx', '4xx', '5xx'])
    expect(Array.from(screen.getByLabelText('Period').options).map((o) => o.textContent.trim())).toEqual(['1 hour', '24 hours', '7 days', '30 days'])
    expect(screen.getByLabelText('Period').value).toBe('24h')
  })

  it('reads the hosts of the period for the select', async () => {
    const { calls } = await open()
    expect(calls.hosts[0]).toMatchObject({ resolution: 'hour', from: '2026-10-27T12:00:00Z', to: '2026-10-28T12:00:00Z' })
  })

  it.each([
    ['2xx', '200', '299'],
    ['3xx', '300', '399'],
    ['4xx', '400', '499'],
    ['5xx', '500', '599'],
  ])('looks for %s by its range of codes', async (label, min, max) => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: label } })
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    expect(calls.logs[1]).toMatchObject({ minStatus: min, maxStatus: max })
  })

  it('stops filtering by status when any status is chosen again', async () => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: '5xx' } })
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: '' } })
    await waitFor(() => expect(calls.logs).toHaveLength(3))
    expect(calls.logs[2].minStatus).toBeUndefined()
    expect(calls.logs[2].maxStatus).toBeUndefined()
  })

  it('looks for one host', async () => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'api.example.com' } })
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    expect(calls.logs[1].host).toBe('api.example.com')
  })

  it('looks for text in the path, once the typing pauses', async () => {
    const { calls } = await open()
    await fireEvent.input(screen.getByLabelText('Search path'), { target: { value: '/api/' } })
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    expect(calls.logs[1].q).toBe('/api/')
  })

  it('does not look again for each key of a word typed quickly', async () => {
    const calls = api()
    render(LogHistory, { now: () => NOW, searchDelay: 40 })
    await screen.findByRole('table', { name: 'Requests' })
    const search = screen.getByLabelText('Search path')
    for (const text of ['/', '/a', '/ap', '/api']) await fireEvent.input(search, { target: { value: text } })
    await waitFor(() => expect(calls.logs.length).toBeGreaterThanOrEqual(2))
    await new Promise((r) => setTimeout(r, 120))
    expect(calls.logs).toHaveLength(2)
    expect(calls.logs[1].q).toBe('/api')
  })

  it.each([
    ['1h', '2026-10-28T11:00:00Z'],
    ['7d', '2026-10-21T12:00:00Z'],
    ['30d', '2026-09-28T12:00:00Z'],
  ])('looks back over %s', async (value, from) => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Period'), { target: { value } })
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    expect(calls.logs[1].from).toBe(from)
  })

  it('reads the hosts of the new period too', async () => {
    const { calls } = await open()
    await fireEvent.change(screen.getByLabelText('Period'), { target: { value: '7d' } })
    await waitFor(() => expect(calls.hosts).toHaveLength(2))
    expect(calls.hosts[1].from).toBe('2026-10-21T12:00:00Z')
  })

  it('starts again from the newest request, with the rows of the new search only', async () => {
    const { calls } = await open({
      logs: (p) => json(p.host ? { entries: [entry(99, { path: '/only-this' })], next: 98 } : { entries, next: 28 }),
    })
    await fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    await fireEvent.change(screen.getByLabelText('Host'), { target: { value: 'app.example.com' } })
    await waitFor(() => expect(calls.logs).toHaveLength(3))
    expect(calls.logs[2].before).toBeUndefined()
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(within(table()).getByText('/only-this')).toBeTruthy()
  })

  it('still searches when the hosts cannot be read', async () => {
    await open({ hosts: () => json({ error: 'internal error' }, 500) })
    expect(screen.getByLabelText('Host').options).toHaveLength(1)
    expect(rows()).toHaveLength(3)
  })

  it('only the answer to the search that is shown counts', async () => {
    const waiting = []
    const { calls } = await open()
    vi.mocked(fetch).mockImplementation((url) => {
      const u = new URL(url, 'http://x')
      if (u.pathname === '/api/logs') return new Promise((resolve) => waiting.push({ params: Object.fromEntries(u.searchParams), resolve }))
      return json(hostsAnswer)
    })
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: '4xx' } })
    await waitFor(() => expect(waiting).toHaveLength(1))
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: '5xx' } })
    await waitFor(() => expect(waiting).toHaveLength(2))

    waiting[1].resolve(ok({ entries: [entry(7, { path: '/the-5xx', status: 500 })], next: null }))
    await waitFor(() => expect(within(table()).getByText('/the-5xx')).toBeTruthy())
    waiting[0].resolve(ok({ entries: [entry(8, { path: '/the-4xx', status: 404 })], next: null }))
    await new Promise((r) => setTimeout(r, 10))
    expect(within(table()).queryByText('/the-4xx')).toBeNull()
    expect(calls.logs).toHaveLength(1)
  })
})

describe('LogHistory paging', () => {
  it('offers more when the API says there are, and adds them below', async () => {
    const more = [entry(27, { path: '/older' })]
    const { calls } = await open({ logs: (p) => json(p.before ? { entries: more, next: null } : { entries, next: 28 }) })
    await fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(rows()).toHaveLength(4))
    expect(calls.logs[1].before).toBe('28')
    expect(cellsOf(rows()[3])[4]).toBe('/older')
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
  })

  it('keeps the filters when it loads more', async () => {
    const { calls } = await open({ logs: (p) => json({ entries, next: p.before ? null : 28 }) })
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: '5xx' } })
    await waitFor(() => expect(calls.logs).toHaveLength(2))
    await fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(calls.logs).toHaveLength(3))
    expect(calls.logs[2]).toMatchObject({ minStatus: '500', maxStatus: '599', before: '28' })
  })

  it('offers nothing more when there is nothing more', async () => {
    await open()
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
  })

  it('does not ask twice while it waits for more', async () => {
    let finish
    const calls = api({ logs: (p) => (p.before ? new Promise((r) => (finish = r)) : json({ entries, next: 28 })) })
    render(LogHistory, { now: () => NOW, searchDelay: 0 })
    const more = await screen.findByRole('button', { name: 'Load more' })
    await fireEvent.click(more)
    expect(more.disabled).toBe(true)
    await fireEvent.click(more)
    expect(calls.logs.filter((p) => p.before)).toHaveLength(1)
    finish(ok({ entries: [], next: null }))
  })
})

describe('LogHistory failing', () => {
  it('says the history is off when its routes are not there, and it is not an error', async () => {
    api({ logs: () => json({ error: 'not found' }, 404), hosts: () => json({ error: 'not found' }, 404) })
    render(LogHistory, { now: () => NOW, searchDelay: 0 })
    expect(await screen.findByText(/traffic history is off/i)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('shows an error when the search fails, and keeps the filters', async () => {
    api({ logs: () => json({ error: 'internal error' }, 500) })
    render(LogHistory, { now: () => NOW, searchDelay: 0 })
    expect((await screen.findByRole('alert')).textContent).toContain('internal error')
    expect(screen.getByLabelText('Search path')).toBeTruthy()
  })

  it('shows an error when caddy-view cannot be reached', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    render(LogHistory, { now: () => NOW, searchDelay: 0 })
    expect((await screen.findByRole('alert')).textContent).toMatch(/caddy-view/)
  })

  it('clears the error when the next search works', async () => {
    let call = 0
    api({ logs: () => (++call === 1 ? json({ error: 'internal error' }, 500) : json({ entries, next: null })) })
    render(LogHistory, { now: () => NOW, searchDelay: 0 })
    await screen.findByRole('alert')
    await fireEvent.change(screen.getByLabelText('Status'), { target: { value: '5xx' } })
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(rows()).toHaveLength(3)
  })
})
