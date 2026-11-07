import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import Dashboard from './Dashboard.svelte'

const reachable = { caddy: { reachable: true, endpoint: 'http://localhost:2019' } }
const down = { caddy: { reachable: false, endpoint: 'http://localhost:2019', error: 'connection refused' } }

const up = (address, state = 'answering', extra = {}) => ({ address, numRequests: 0, fails: 0, state, ...extra })

const sites = {
  upstreamsAvailable: true,
  upstreamCheck: true,
  healthChecks: { proxies: 3, withChecks: 0 },
  sites: [
    { server: 'srv0', host: 'app.example.com', upstreams: [up('10.0.0.5:8080', 'answering', { latencyMs: 12.4, httpStatus: 200 })] },
    { server: 'srv0', host: 'admin.example.com', upstreams: [up('10.0.0.9:3000', 'down')] },
    { server: 'srv0', host: 'docs.example.com', upstreams: [up('10.0.0.4:4000', 'answering', { latencyMs: 3, httpStatus: 404 })] },
    { server: 'srv0', host: 'files.example.com', upstreams: [] },
  ],
}

const summary = {
  window: '24h',
  requests: 6811,
  errors5xx: 41,
  errorRate: 0.006,
  avgMs: 42.3,
  p95Ms: 310,
  bytes: 30828134,
  status: { '2xx': 6000 },
  inFlight: 3,
  upstreams: { healthy: 4, total: 5 },
  previous: { requests: 6425, errors5xx: 12, errorRate: 0.0019, avgMs: 40, p95Ms: 300 },
}

const hour = (h, requests) => ({ start: new Date(2026, 9, 28, h).toISOString(), requests, bytes: 0, status: {} })
const traffic = { resolution: 'hour', bucketSeconds: 3600, buckets: [hour(8, 100), hour(9, 400)] }

const figure = (name) => screen.getByRole('group', { name })

describe('Dashboard state', () => {
  it('says Caddy is reachable, with the Admin API address', () => {
    render(Dashboard, { status: reachable, sites })
    const chip = screen.getByRole('status')
    expect(chip.textContent).toContain('Caddy is reachable')
    expect(chip.dataset.state).toBe('up')
    expect(screen.getByText(/http:\/\/localhost:2019/)).toBeTruthy()
  })

  it('says Caddy is not reachable and why', () => {
    render(Dashboard, { status: down, sites: null })
    const chip = screen.getByRole('status')
    expect(chip.textContent).toContain('Caddy is not reachable')
    expect(chip.textContent).toContain('connection refused')
    expect(chip.dataset.state).toBe('down')
  })

  it('shows a loading message before the first answer', () => {
    render(Dashboard, { status: null, sites: null })
    expect(screen.getByText('Loading…')).toBeTruthy()
  })

  it('shows an error when caddy-view itself cannot be reached', () => {
    render(Dashboard, { status: null, sites: null, loadError: 'cannot reach caddy-view' })
    expect(screen.getByRole('alert').textContent).toContain('cannot reach caddy-view')
  })

  it('has a title, and says what the figures are compared with', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, range: '24h' })
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeTruthy()
    expect(screen.getByText(/compared with the previous 24 hours/i)).toBeTruthy()
  })
})

describe('Dashboard range', () => {
  it('offers 1 hour, 24 hours and 7 days, with the current one chosen', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, range: '24h' })
    const group = screen.getByRole('radiogroup', { name: 'Range' })
    expect(within(group).getAllByRole('radio').map((r) => r.textContent)).toEqual(['1h', '24h', '7d'])
    expect(within(group).getByRole('radio', { name: '24h' }).getAttribute('aria-checked')).toBe('true')
  })

  it('tells the shell when another is chosen', async () => {
    const onrange = vi.fn()
    render(Dashboard, { status: reachable, sites, summary, traffic, range: '24h', onrange })
    await fireEvent.click(screen.getByRole('radio', { name: '7d' }))
    expect(onrange).toHaveBeenCalledWith('7d')
  })
})

describe('Dashboard key figures', () => {
  it('shows requests with how they moved against the previous period', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(within(figure('Requests')).getByText('6,811')).toBeTruthy()
    expect(within(figure('Requests')).getByText('+6% on the previous period')).toBeTruthy()
  })

  it('shows the 5xx rate in times, and marks it when it doubled', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    const f = figure('5xx rate')
    expect(within(f).getByText('0.6 %')).toBeTruthy()
    expect(within(f).getByText(/41 of 6,811 requests/)).toBeTruthy()
    expect(within(f).getByText('3.2x the previous period')).toBeTruthy()
    expect(f.dataset.tone).toBe('bad')
  })

  it('shows the p95 latency with the average', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(within(figure('p95 latency')).getByText('310 ms')).toBeTruthy()
    expect(within(figure('p95 latency')).getByText('average 42 ms')).toBeTruthy()
  })

  it('says nothing about the previous period when there is none', () => {
    const none = { requests: 0, errors5xx: 0, errorRate: null, avgMs: null, p95Ms: null }
    render(Dashboard, { status: reachable, sites, summary: { ...summary, previous: none }, traffic })
    expect(within(figure('Requests')).queryByText(/on the previous period/)).toBeNull()
  })

  it('shows an en dash for what does not exist yet', () => {
    const empty = { ...summary, requests: 0, errors5xx: 0, errorRate: null, avgMs: null, p95Ms: null, bytes: 0 }
    render(Dashboard, { status: reachable, sites, summary: empty, traffic: { ...traffic, buckets: [] } })
    expect(within(figure('5xx rate')).getByText('–')).toBeTruthy()
    expect(within(figure('p95 latency')).getByText('–')).toBeTruthy()
  })

  it('counts the upstreams that answer, with a strip of one segment each, failing first', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    const f = figure('Upstreams')
    expect(within(f).getByText('2 / 3')).toBeTruthy()
    expect(within(f).getByText('1 down')).toBeTruthy()
    const strip = within(f).getByRole('img')
    expect(strip.getAttribute('aria-label')).toBe('2 answering, 1 down')
    expect(Array.from(strip.children).map((s) => s.dataset.state)).toEqual(['down', 'answering', 'answering'])
  })

  it('says all answering when none is down', () => {
    const fine = { ...sites, sites: [sites.sites[0]] }
    render(Dashboard, { status: reachable, sites: fine, summary, traffic })
    expect(within(figure('Upstreams')).getByText('all answering')).toBeTruthy()
  })

  it('counts unknown upstreams apart, never as down', () => {
    const far = { ...sites, sites: [{ server: 's', host: 'far.example.com', upstreams: [up('172.16.0.1:80', 'unknown')] }] }
    render(Dashboard, { status: reachable, sites: far, summary, traffic })
    expect(within(figure('Upstreams')).getByText('1 unknown')).toBeTruthy()
    expect(within(figure('Upstreams')).queryByText(/down/)).toBeNull()
  })

  it('falls back to what Caddy reports when the check is off', () => {
    const off = { ...sites, upstreamCheck: false, sites: sites.sites.map((s) => ({ ...s, upstreams: s.upstreams.map((u) => ({ ...u, state: 'unknown' })) })) }
    render(Dashboard, { status: reachable, sites: off, summary, traffic })
    const f = figure('Upstreams')
    expect(within(f).getByText('4 / 5')).toBeTruthy()
    expect(within(f).getByText('healthy, as Caddy says')).toBeTruthy()
  })

  it('has no upstream figure without any information', () => {
    render(Dashboard, { status: reachable, sites: { ...sites, sites: [], upstreamCheck: false }, summary: { ...summary, upstreams: null }, traffic })
    expect(screen.queryByRole('group', { name: 'Upstreams' })).toBeNull()
  })
})

describe('Dashboard history off', () => {
  it('explains there are no totals or charts', () => {
    render(Dashboard, { status: reachable, sites, historyOff: true })
    expect(screen.getByText(/traffic history is off/i)).toBeTruthy()
    expect(screen.queryByRole('group', { name: 'Requests' })).toBeNull()
  })
})

describe('Dashboard chart', () => {
  it('draws the requests over time', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.getByRole('img', { name: /requests per hour/i })).toBeTruthy()
  })

  it('names the minute when it is by the minute', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic: { ...traffic, resolution: 'minute' }, range: '1h' })
    expect(screen.getByRole('img', { name: /requests per minute/i })).toBeTruthy()
  })
})

describe('Dashboard chart, against the previous period', () => {
  it('is a line, with the previous period beside it when it is known', () => {
    const { container } = render(Dashboard, { status: reachable, sites, summary, traffic, previousTraffic: traffic })
    expect(container.querySelector('path[data-series="current"]')).toBeTruthy()
    expect(container.querySelector('rect[data-bar]')).toBeNull()
    expect(screen.getByText('Previous period')).toBeTruthy()
  })

  it('has no previous line without the previous period', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.queryByText('Previous period')).toBeNull()
  })
})

describe('Dashboard status codes', () => {
  const codes = () => within(screen.getByRole('region', { name: 'Status codes' }))
  const withStatus = { ...summary, requests: 1000, status: { '2xx': 941, '3xx': 32, '4xx': 24, '5xx': 3, '1xx': 0, other: 0 } }

  it('shows the share of each class that has requests', () => {
    render(Dashboard, { status: reachable, sites, summary: withStatus, traffic })
    const rows = codes().getAllByRole('listitem')
    expect(rows.map((r) => Array.from(r.querySelectorAll('span')).map((x) => x.textContent))).toEqual([
      ['2xx', '94.1 %'],
      ['3xx', '3.2 %'],
      ['4xx', '2.4 %'],
      ['5xx', '0.3 %'],
    ])
  })

  it('draws them as one bar, with the shares for those who cannot see it', () => {
    render(Dashboard, { status: reachable, sites, summary: withStatus, traffic })
    expect(codes().getByRole('img').getAttribute('aria-label')).toContain('94.1 % 2xx')
  })

  it('is left out without requests', () => {
    render(Dashboard, { status: reachable, sites, summary: { ...summary, requests: 0, status: {} }, traffic })
    expect(screen.queryByRole('region', { name: 'Status codes' })).toBeNull()
  })
})

describe('Dashboard top hosts', () => {
  const hosts = [
    { host: 'app.example.com', requests: 4000 },
    { host: 'api.example.com', requests: 2000 },
    { host: 'static.example.com', requests: 811 },
  ]
  const top = () => within(screen.getByRole('region', { name: 'Top hosts' }))

  it('lists the busiest hosts with how many requests each had', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, hosts })
    const rows = top().getAllByRole('listitem')
    expect(rows.map((r) => Array.from(r.querySelectorAll('span')).map((x) => x.textContent))).toEqual([
      ['app.example.com', '4,000'],
      ['api.example.com', '2,000'],
      ['static.example.com', '811'],
    ])
  })

  it('draws each as a share of the requests of the whole period', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, hosts })
    const bars = top().getAllByRole('listitem').map((r) => r.querySelector('.thin i').style.width)
    expect(bars[0]).toBe('58.7%')
    expect(Number.parseFloat(bars[1])).toBeCloseTo(29.4, 1)
  })

  it('renders hostile names as text', () => {
    const { container } = render(Dashboard, { status: reachable, sites, summary, traffic, hosts: [{ host: '<img src=x>', requests: 5 }] })
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('<img src=x>')).toBeTruthy()
  })

  it('says so when there was no traffic from any host', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, hosts: [] })
    expect(top().getByText(/no traffic/i)).toBeTruthy()
  })

  it('is left out when the hosts could not be read', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.queryByRole('region', { name: 'Top hosts' })).toBeNull()
  })
})

describe('Dashboard needs attention', () => {
  const items = [
    { kind: 'upstream_down', severity: 'bad', host: 'admin.example.com', down: ['10.0.9.9:3000'], total: 1 },
    { kind: 'no_health_checks', severity: 'info' },
  ]

  it('lists the items with what is wrong', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, attention: items })
    const section = screen.getByRole('region', { name: 'Needs attention' })
    expect(within(section).getByText('admin.example.com is down')).toBeTruthy()
    expect(within(section).getByText(/Its only upstream, 10.0.9.9:3000, does not answer/)).toBeTruthy()
    expect(within(section).getByText('Caddy has no health checks')).toBeTruthy()
  })

  it('marks each item with its severity', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, attention: items })
    const section = screen.getByRole('region', { name: 'Needs attention' })
    expect(Array.from(section.querySelectorAll('[data-severity]')).map((e) => e.dataset.severity)).toEqual(['bad', 'info'])
  })

  it('says nothing needs attention when it does not', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, attention: [] })
    expect(within(screen.getByRole('region', { name: 'Needs attention' })).getByText(/nothing needs attention/i)).toBeTruthy()
  })

  it('explains how it decides, once, in a tooltip on the title', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, attention: [] })
    const section = screen.getByRole('region', { name: 'Needs attention' })
    const help = within(section).getByRole('button', { name: /how this is decided/i })
    expect(help.querySelector('[role="tooltip"]').textContent).toMatch(/median/i)
  })

  it('tells what a native health check of Caddy would need, from the statuses seen', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic, attention: items })
    const section = screen.getByRole('region', { name: 'Needs attention' })
    // docs.example.com answers 404 on /, and a default check of Caddy counts only 2xx
    expect(within(section).getByText(/1 of your 3 upstreams does not answer 2xx on \//)).toBeTruthy()
  })

  it('has no section without attention data', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.queryByRole('region', { name: 'Needs attention' })).toBeNull()
  })
})

describe('Dashboard details row', () => {
  const hosts = [{ host: 'app.example.com', requests: 4000 }]
  const withStatus = { ...summary, requests: 1000, status: { '2xx': 941, '3xx': 32, '4xx': 24, '5xx': 3 } }

  it('has status codes, top hosts and the sites and upstreams side by side, in one row', () => {
    render(Dashboard, { status: reachable, sites, summary: withStatus, traffic, hosts })
    const row = screen.getByRole('region', { name: 'Details' })
    for (const name of ['Status codes', 'Top hosts', 'Sites and upstreams']) {
      expect(within(row).getByRole('region', { name })).toBeTruthy()
    }
  })

  it('still has the sites and upstreams when there is no history', () => {
    render(Dashboard, { status: reachable, sites, historyOff: true })
    const row = screen.getByRole('region', { name: 'Details' })
    expect(within(row).getByRole('region', { name: 'Sites and upstreams' })).toBeTruthy()
    expect(within(row).queryByRole('region', { name: 'Status codes' })).toBeNull()
  })

  it('sums the upstreams up above the list, worst first', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    const caption = within(screen.getByRole('region', { name: 'Sites and upstreams' })).getByText(/worst first/)
    expect(caption.textContent).toBe('1 down · 2 answering · worst first')
  })

  it('counts the unknown ones apart in that line', () => {
    const far = { ...sites, sites: [{ server: 's', host: 'far.example.com', upstreams: [up('172.16.0.1:80', 'unknown')] }] }
    render(Dashboard, { status: reachable, sites: far, summary, traffic })
    expect(within(screen.getByRole('region', { name: 'Sites and upstreams' })).getByText(/worst first/).textContent).toBe('1 unknown · worst first')
  })
})

describe('Dashboard sites and upstreams', () => {
  const list = () => within(screen.getByRole('region', { name: 'Sites and upstreams' }))

  it('lists the upstreams failing first, each with its host and address', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    const rows = list().getAllByRole('listitem')
    expect(rows.map((r) => r.dataset.state)).toEqual(['down', 'answering', 'answering'])
    expect(within(rows[0]).getByText('admin.example.com')).toBeTruthy()
    expect(within(rows[0]).getByText('10.0.0.9:3000')).toBeTruthy()
    expect(rows[0].textContent).toContain('down')
  })

  it('shows the latency of an answering upstream', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    const row = screen.getByText('app.example.com').closest('li')
    expect(row.textContent).toContain('answering')
    expect(row.textContent).toContain('12 ms')
  })

  it('says what a non-2xx answer was', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.getByText('docs.example.com').closest('li').textContent).toContain('answers 404')
  })

  it('shows unknown as unknown, not as a problem', () => {
    const far = { ...sites, sites: [{ server: 's', host: 'far.example.com', upstreams: [up('172.16.0.1:80', 'unknown')] }] }
    render(Dashboard, { status: reachable, sites: far, summary, traffic })
    const row = screen.getByText('far.example.com').closest('li')
    expect(row.dataset.state).toBe('unknown')
    expect(row.textContent).toContain('unknown')
  })

  it('names a catch-all site', () => {
    const any = { ...sites, sites: [{ server: 's', host: '', upstreams: [up('a:1')] }] }
    render(Dashboard, { status: reachable, sites: any, summary, traffic })
    expect(screen.getByText('(any host)')).toBeTruthy()
  })

  it('renders hostile names as text', () => {
    const hostile = { ...sites, sites: [{ server: 's', host: '<img src=x onerror=alert(1)>', upstreams: [up('a:1')] }] }
    const { container } = render(Dashboard, { status: reachable, sites: hostile, summary, traffic })
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeTruthy()
  })

  it('says so when Caddy has no upstreams', () => {
    render(Dashboard, { status: reachable, sites: { ...sites, sites: [sites.sites[3]] }, summary, traffic })
    expect(screen.getByText(/no upstreams/i)).toBeTruthy()
  })

  it('does not show sites while Caddy is down', () => {
    render(Dashboard, { status: down, sites: null })
    expect(screen.queryByRole('region', { name: 'Sites and upstreams' })).toBeNull()
  })

  describe('with many upstreams', () => {
    const many = {
      ...sites,
      sites: Array.from({ length: 20 }, (_, i) => ({ server: 's', host: `h${i}.example.com`, upstreams: [up(`10.0.0.${i}:80`, i === 7 ? 'down' : 'answering')] })),
    }

    it('shows the down one and a few more, behind a link for the rest', () => {
      render(Dashboard, { status: reachable, sites: many, summary, traffic })
      expect(list().getAllByRole('listitem')).toHaveLength(5)
      expect(list().getAllByRole('listitem')[0].dataset.state).toBe('down')
      expect(list().getByRole('button', { name: 'Show 15 more' })).toBeTruthy()
    })

    it('opens the rest in place, and goes back with Show less', async () => {
      render(Dashboard, { status: reachable, sites: many, summary, traffic })
      await fireEvent.click(list().getByRole('button', { name: 'Show 15 more' }))
      expect(list().getAllByRole('listitem')).toHaveLength(20)
      await fireEvent.click(list().getByRole('button', { name: 'Show less' }))
      expect(list().getAllByRole('listitem')).toHaveLength(5)
    })
  })
})
