import { render, screen, within } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import Dashboard from './Dashboard.svelte'

const reachable = { caddy: { reachable: true, endpoint: 'http://localhost:2019' } }
const down = { caddy: { reachable: false, endpoint: 'http://localhost:2019', error: 'connection refused' } }

const sites = {
  upstreamsAvailable: true,
  sites: [
    { server: 'srv1', host: 'app.example.com', upstreams: [{ address: '10.0.0.5:8080', numRequests: 3, fails: 0 }] },
    {
      server: 'srv1',
      host: 'api.example.com',
      upstreams: [
        { address: '10.0.0.6:9000', numRequests: 0, fails: 2 },
        { address: '10.0.0.7:9000', numRequests: 1, fails: 0 },
      ],
    },
    { server: 'srv0', host: 'static.example.com', upstreams: [] },
  ],
}

describe('Dashboard status', () => {
  it('says Caddy is reachable and shows the Admin API address', () => {
    render(Dashboard, { status: reachable, sites })
    const banner = screen.getByRole('status')
    expect(banner.textContent).toContain('Caddy is reachable')
    expect(banner.textContent).toContain('http://localhost:2019')
  })

  it('says Caddy is not reachable and why', () => {
    render(Dashboard, { status: down, sites: null })
    const banner = screen.getByRole('status')
    expect(banner.textContent).toContain('Caddy is not reachable')
    expect(banner.textContent).toContain('connection refused')
    expect(banner.dataset.state).toBe('down')
  })

  it('marks a reachable Caddy so it can be styled differently', () => {
    render(Dashboard, { status: reachable, sites })
    expect(screen.getByRole('status').dataset.state).toBe('up')
  })

  it('shows a loading message before the first answer', () => {
    render(Dashboard, { status: null, sites: null })
    expect(screen.getByText('Loading…')).toBeTruthy()
  })

  it('shows an error when caddy-view itself cannot be reached', () => {
    render(Dashboard, { status: null, sites: null, loadError: 'cannot reach caddy-view' })
    expect(screen.getByRole('alert').textContent).toContain('cannot reach caddy-view')
  })
})

describe('Dashboard sites', () => {
  it('lists every site with its server and upstreams', () => {
    render(Dashboard, { status: reachable, sites })
    const rows = screen.getAllByRole('row').slice(1) // the first row is the header
    expect(rows).toHaveLength(3)

    const app = within(rows[0])
    expect(app.getByText('app.example.com')).toBeTruthy()
    expect(app.getByText('srv1')).toBeTruthy()
    expect(app.getByText('10.0.0.5:8080')).toBeTruthy()
    expect(rows[0].textContent).toContain('3 active')
    expect(rows[0].textContent).toContain('0 failures')
  })

  it('flags an upstream that has failures', () => {
    render(Dashboard, { status: reachable, sites })
    const failing = screen.getByText('10.0.0.6:9000').closest('[data-health]')
    const healthy = screen.getByText('10.0.0.7:9000').closest('[data-health]')
    expect(failing.dataset.health).toBe('failing')
    expect(healthy.dataset.health).toBe('ok')
    expect(failing.textContent).toContain('2 failures')
  })

  it('uses the singular for one failure', () => {
    const one = { upstreamsAvailable: true, sites: [{ server: 's', host: 'h', upstreams: [{ address: 'a:1', numRequests: 0, fails: 1 }] }] }
    render(Dashboard, { status: reachable, sites: one })
    expect(screen.getByText('a:1').closest('[data-health]').textContent).toContain('1 failure')
    expect(screen.getByText('a:1').closest('[data-health]').textContent).not.toContain('1 failures')
  })

  it('shows a site without upstreams as such', () => {
    render(Dashboard, { status: reachable, sites })
    const row = screen.getByText('static.example.com').closest('tr')
    expect(row.textContent).toContain('No upstream')
  })

  it('says so when Caddy has no sites', () => {
    render(Dashboard, { status: reachable, sites: { sites: [], upstreamsAvailable: true } })
    expect(screen.getByText('No sites found')).toBeTruthy()
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('warns that upstream health is unknown instead of showing zeros', () => {
    render(Dashboard, { status: reachable, sites: { ...sites, upstreamsAvailable: false } })
    expect(screen.getByText(/upstream health is unavailable/i)).toBeTruthy()
    expect(screen.queryByText(/active/)).toBeNull()
    expect(screen.queryByText(/failure/)).toBeNull()
    expect(screen.getByText('10.0.0.5:8080')).toBeTruthy()
  })

  it('does not show sites while Caddy is down', () => {
    render(Dashboard, { status: down, sites: null })
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('renders hostile names as text', () => {
    const hostile = {
      upstreamsAvailable: true,
      sites: [{ server: 's', host: '<img src=x onerror=alert(1)>', upstreams: [] }],
    }
    const { container } = render(Dashboard, { status: reachable, sites: hostile })
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeTruthy()
  })
})

describe('Dashboard sites without a host', () => {
  it('shows a catch-all site as "any host" instead of an empty cell', () => {
    const catchAll = { upstreamsAvailable: true, sites: [{ server: 'srv0', host: '', upstreams: [] }] }
    render(Dashboard, { status: reachable, sites: catchAll })
    expect(screen.getByText('(any host)')).toBeTruthy()
  })
})

const summary = {
  window: '24h',
  requests: 6811,
  requestsPerSecond: 0.08,
  errors5xx: 41,
  errorRate: 0.006,
  avgMs: 42.3,
  p95Ms: 310,
  bytes: 30828134,
  status: { '2xx': 6000, '3xx': 400, '4xx': 370, '5xx': 41, '1xx': 0, other: 0 },
  inFlight: 3,
  upstreams: { healthy: 4, total: 5 },
}

const hour = (h, requests) => ({ start: new Date(2026, 9, 28, h).toISOString(), requests, bytes: 0, status: {} })
const traffic = { resolution: 'hour', bucketSeconds: 3600, buckets: [hour(8, 100), hour(9, 400)] }

const tile = (name) => screen.getByRole('group', { name })

describe('Dashboard header', () => {
  it('has a page title and a one-line subtitle', () => {
    render(Dashboard, { status: reachable, sites })
    expect(screen.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeTruthy()
    expect(screen.getByText(/the last 24 hours and your sites at a glance/i)).toBeTruthy()
  })
})

describe('Dashboard tiles', () => {
  it('shows the totals of the last 24 hours', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.getByRole('heading', { name: 'Last 24 hours' })).toBeTruthy()
    expect(within(tile('Requests')).getByText('6,811')).toBeTruthy()
    expect(within(tile('Requests')).getByText('3 in flight')).toBeTruthy()
    expect(within(tile('Traffic')).getByText('29.4 MB')).toBeTruthy()
    expect(within(tile('Server errors')).getByText('0.6 %')).toBeTruthy()
    expect(within(tile('Server errors')).getByText('41 requests')).toBeTruthy()
    expect(within(tile('Avg. response time')).getByText('42 ms')).toBeTruthy()
    expect(within(tile('Avg. response time')).getByText('p95 310 ms')).toBeTruthy()
  })

  it('says how many upstreams are healthy when Caddy reports them', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(tile('Upstreams').textContent).toBe('Upstreams4 of 5healthy')
  })

  it('marks the upstreams tile when one is down', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(tile('Upstreams').dataset.tone).toBe('warn')
  })

  it('leaves out what it does not know instead of showing zeros', () => {
    const quiet = { ...summary, inFlight: null, upstreams: null }
    render(Dashboard, { status: reachable, sites, summary: quiet, traffic })
    expect(screen.queryByRole('group', { name: 'Upstreams' })).toBeNull()
    expect(screen.queryByText(/in flight/)).toBeNull()
  })

  it('shows an en dash for averages that do not exist yet', () => {
    const empty = { ...summary, requests: 0, errors5xx: 0, errorRate: null, avgMs: null, p95Ms: null, bytes: 0 }
    render(Dashboard, { status: reachable, sites, summary: empty, traffic: { ...traffic, buckets: [] } })
    expect(within(tile('Server errors')).getByText('–')).toBeTruthy()
    expect(within(tile('Avg. response time')).getByText('–')).toBeTruthy()
    expect(within(tile('Requests')).getByText('0')).toBeTruthy()
  })

  it('uses the singular for one failing request', () => {
    render(Dashboard, { status: reachable, sites, summary: { ...summary, errors5xx: 1 }, traffic })
    expect(within(tile('Server errors')).getByText('1 request')).toBeTruthy()
  })

  it('marks a tile with server errors so it can be coloured', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(tile('Server errors').dataset.tone).toBe('bad')
    expect(tile('Requests').hasAttribute('data-tone')).toBe(false)
  })

  it('does not colour the errors tile when there are none', () => {
    render(Dashboard, { status: reachable, sites, summary: { ...summary, errors5xx: 0, errorRate: 0 }, traffic })
    expect(tile('Server errors').hasAttribute('data-tone')).toBe(false)
  })
})

describe('Dashboard traffic chart', () => {
  it('draws requests per hour', () => {
    const { container } = render(Dashboard, { status: reachable, sites, summary, traffic })
    expect(screen.getByRole('img').getAttribute('aria-label')).toContain('Requests per hour')
    expect(container.querySelectorAll('rect[data-bar]')).toHaveLength(2)
  })

  it('has no chart before the traffic has been read', () => {
    render(Dashboard, { status: reachable, sites, summary, traffic: null })
    expect(screen.queryByRole('img')).toBeNull()
  })
})

describe('Dashboard without history', () => {
  it('says the traffic history is off instead of showing tiles and a chart', () => {
    render(Dashboard, { status: reachable, sites, historyOff: true })
    expect(screen.getByText(/traffic history is off/i)).toBeTruthy()
    expect(screen.queryByRole('group')).toBeNull()
    expect(screen.queryByRole('img')).toBeNull()
  })

  it('still shows the sites', () => {
    render(Dashboard, { status: reachable, sites, historyOff: true })
    expect(screen.getByText('app.example.com')).toBeTruthy()
  })

  it('says nothing about history by default', () => {
    render(Dashboard, { status: reachable, sites })
    expect(screen.queryByText(/traffic history is off/i)).toBeNull()
  })
})
