import { render, screen, within } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import StatusBar from './StatusBar.svelte'

const status = { '1xx': 0, '2xx': 90, '3xx': 0, '4xx': 9, '5xx': 1, other: 0 }

describe('StatusBar', () => {
  it('has a segment for each class that has requests, as wide as its share', () => {
    const { container } = render(StatusBar, { status })
    const segments = Array.from(container.querySelectorAll('[data-class]')).filter((el) => el.closest('.bar'))
    expect(segments.map((s) => s.dataset.class)).toEqual(['2xx', '4xx', '5xx'])
    expect(segments.map((s) => parseFloat(s.style.width))).toEqual([90, 9, 1].map((n) => expect.closeTo(n, 1)))
  })

  it('describes the bar for those who cannot see it', () => {
    render(StatusBar, { status })
    const label = screen.getByRole('img').getAttribute('aria-label')
    expect(label).toContain('Status codes')
    expect(label).toContain('Success 90 %')
    expect(label).toContain('Client error 9 %')
    expect(label).toContain('Server error 1 %')
  })

  it('lists each class with its name, its count and its share', () => {
    render(StatusBar, { status })
    const table = screen.getByRole('table', { name: 'Status codes' })
    const rows = within(table).getAllByRole('row').slice(1) // the first row is the header
    expect(rows.map((r) => Array.from(r.querySelectorAll('td')).map((c) => c.textContent.trim()).join(' '))).toEqual([
      '2xx Success 90 90 %',
      '4xx Client error 9 9 %',
      '5xx Server error 1 1 %',
    ])
  })

  it('writes the counts with thousands separators', () => {
    render(StatusBar, { status: { '2xx': 12345, '5xx': 5 } })
    expect(screen.getByText('12,345')).toBeTruthy()
  })

  it('does not hide a class that is a tiny share', () => {
    const { container } = render(StatusBar, { status: { '2xx': 100000, '5xx': 1 } })
    expect(container.querySelectorAll('.bar [data-class]')).toHaveLength(2)
    expect(screen.getByRole('table', { name: 'Status codes' }).textContent).toContain('<0.1 %')
  })

  it.each([[{}], [null], [{ '2xx': 0, '5xx': 0 }]])('says there are no requests for %j', (empty) => {
    const { container } = render(StatusBar, { status: empty })
    expect(screen.getByText('No requests')).toBeTruthy()
    expect(container.querySelector('.bar')).toBeNull()
    expect(screen.queryByRole('table')).toBeNull()
  })
})
