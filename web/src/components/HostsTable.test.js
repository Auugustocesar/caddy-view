import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import HostsTable from './HostsTable.svelte'

const hosts = [
  { host: 'app.example.com', requests: 1547, bytes: 30828134, errors5xx: 0, status: { '2xx': 1200, '3xx': 300, '4xx': 47, '5xx': 0 }, avgMs: 42.3, p95Ms: 100, maxMs: 800 },
  { host: 'api.example.com', requests: 312, bytes: 2048, errors5xx: 18, status: { '2xx': 280, '5xx': 18, '4xx': 14 }, avgMs: 1500, p95Ms: 5000, maxMs: 9000 },
  { host: '', requests: 4, bytes: 0, errors5xx: 0, status: {}, avgMs: null, p95Ms: null, maxMs: null },
]

const open = (props = {}) => {
  const onselect = vi.fn()
  const view = render(HostsTable, { hosts, onselect, ...props })
  return { ...view, onselect }
}

const rowOf = (host) => screen.getByText(host).closest('tr')

describe('HostsTable', () => {
  it('has a table named Hosts with the columns the Metrics tab reads', () => {
    open()
    const table = screen.getByRole('table', { name: 'Hosts' })
    expect(within(table).getAllByRole('columnheader').map((h) => h.textContent.trim())).toEqual([
      'Host',
      'Requests',
      'p95',
      '5xx',
      'Sent',
      'Status codes',
    ])
  })

  it('shows each host with its figures laid out for reading', () => {
    open()
    const cells = Array.from(rowOf('app.example.com').querySelectorAll('td')).map((c) => c.textContent.trim())
    expect(cells).toEqual(['app.example.com', '1,547', '100 ms', '0', '29.4 MB', ''])
    const slow = Array.from(rowOf('api.example.com').querySelectorAll('td')).map((c) => c.textContent.trim())
    expect(slow).toEqual(['api.example.com', '312', '5 s', '18', '2.0 KB', ''])
  })

  it('keeps the order it is given', () => {
    open()
    const names = screen.getAllByRole('row').slice(1).map((r) => r.querySelector('td').textContent.trim())
    expect(names).toEqual(['app.example.com', 'api.example.com', '(no host)'])
  })

  it('shows a request without a host as such, and a missing latency as a dash', () => {
    open()
    const cells = Array.from(rowOf('(no host)').querySelectorAll('td')).map((c) => c.textContent.trim())
    expect(cells).toEqual(['(no host)', '4', '–', '0', '0 B', ''])
  })

  it('marks the hosts that had server errors', () => {
    open()
    expect(rowOf('api.example.com').dataset.errors).toBe('true')
    expect(rowOf('app.example.com').hasAttribute('data-errors')).toBe(false)
  })

  it('draws the status classes of a host as one bar, and says them for those who cannot see it', () => {
    open()
    const bar = within(rowOf('app.example.com')).getByRole('img')
    expect(bar.getAttribute('aria-label')).toBe('77.6 % 2xx, 19.4 % 3xx, 3 % 4xx')
    expect(Array.from(bar.children).map((c) => c.dataset.class)).toEqual(['2xx', '3xx', '4xx'])
  })

  it('draws no bar for a host without statuses', () => {
    open()
    expect(within(rowOf('(no host)')).queryByRole('img')).toBeNull()
  })

  it('asks to filter by a host when its name is clicked', async () => {
    const { onselect } = open()
    await fireEvent.click(screen.getByRole('button', { name: 'app.example.com' }))
    expect(onselect).toHaveBeenCalledExactlyOnceWith('app.example.com')
  })

  it('marks the host that is the filter, and clears it when that one is clicked', async () => {
    const { onselect } = open({ selected: 'app.example.com' })
    const button = screen.getByRole('button', { name: 'app.example.com' })
    expect(button.getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByRole('button', { name: 'api.example.com' }).getAttribute('aria-pressed')).toBe('false')
    await fireEvent.click(button)
    expect(onselect).toHaveBeenCalledExactlyOnceWith('')
  })

  it('cannot filter by the empty host, because the API takes no host to mean all of them', () => {
    open()
    expect(screen.queryByRole('button', { name: '(no host)' })).toBeNull()
  })

  it('says so when there is no traffic', () => {
    render(HostsTable, { hosts: [] })
    expect(screen.getByText('No traffic in this period')).toBeTruthy()
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('renders hostile names as text', () => {
    const { container } = render(HostsTable, { hosts: [{ ...hosts[0], host: '<img src=x onerror=alert(1)>' }] })
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeTruthy()
  })
})
