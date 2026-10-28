import { render, screen, within } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import BarChart from './BarChart.svelte'

const hour = (h, requests) => ({ start: new Date(2026, 9, 28, h).toISOString(), requests })

const buckets = [hour(8, 100), hour(9, 400), hour(10, 0), hour(11, 200)]

const bars = (container) => Array.from(container.querySelectorAll('rect[data-bar]'))
const heightOf = (bar) => Number(bar.getAttribute('height'))

describe('BarChart bars', () => {
  it('draws one bar per bucket', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets })
    expect(bars(container)).toHaveLength(4)
  })

  it('makes a bar as tall as its share of the busiest one', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets })
    const [a, b, c, d] = bars(container).map(heightOf)
    expect(b).toBeGreaterThan(0)
    expect(a).toBeCloseTo(b / 4, 5)
    expect(d).toBeCloseTo(b / 2, 5)
    expect(c).toBe(0)
  })

  it('keeps the bars in time order, left to right', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets })
    const xs = bars(container).map((bar) => Number(bar.getAttribute('x')))
    expect(xs).toEqual([...xs].sort((p, q) => p - q))
    expect(new Set(xs).size).toBe(4)
  })

  it('shows the top of the axis as a round number', () => {
    render(BarChart, { title: 'Requests per hour', buckets })
    expect(screen.getByText('500')).toBeTruthy()
  })
})

describe('BarChart accessibility', () => {
  it('is an image named by its title and its total', () => {
    render(BarChart, { title: 'Requests per hour', buckets })
    const image = screen.getByRole('img')
    expect(image.getAttribute('aria-label')).toContain('Requests per hour')
    expect(image.getAttribute('aria-label')).toContain('700')
  })

  it('has the same numbers in a table, closed until asked for', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets })
    const details = container.querySelector('details')
    expect(details.open).toBe(false)
    expect(within(details).getByText('As table')).toBeTruthy()

    const rows = within(details).getAllByRole('row', { hidden: true }).slice(1) // the first row is the header
    expect(rows.map((row) => row.textContent)).toEqual(['08:00100', '09:00400', '10:000', '11:00200'])
  })

  it('labels the table with the title', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets })
    expect(container.querySelector('details table caption').textContent).toBe('Requests per hour')
  })
})

describe('BarChart over several days', () => {
  // Hourly buckets from 10:00 on 28 October, for three days.
  const days = Array.from({ length: 72 }, (_, i) => ({ start: new Date(2026, 9, 28, 10 + i).toISOString(), requests: 10 + (i % 24) }))

  const axisDays = (container) =>
    Array.from(container.querySelectorAll('svg text.axis'))
      .map((t) => t.textContent)
      .filter((text) => /Oct|Nov/.test(text))

  it('labels the start of each day, not every few hours', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: days, labels: 'date' })
    expect(axisDays(container)).toEqual(['28 Oct', '29 Oct', '30 Oct', '31 Oct'])
  })

  it('does not put a time of day on the axis when it labels days', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: days, labels: 'date' })
    const labels = Array.from(container.querySelectorAll('svg text.axis')).map((t) => t.textContent)
    expect(labels.filter((text) => /^\d\d:\d\d$/.test(text))).toEqual([])
  })

  it('keeps the time of day on the axis by default', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: days })
    expect(axisDays(container)).toEqual([])
  })

  it('puts the date and the time in the table, so a row is never ambiguous', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: days, labels: 'date' })
    const rows = Array.from(container.querySelectorAll('details tbody tr')).map((r) => r.querySelector('td').textContent)
    expect(rows.slice(0, 2)).toEqual(['28 Oct 10:00', '28 Oct 11:00'])
    expect(rows[14]).toBe('29 Oct 00:00')
  })

  it('names the bar with the date and the time too', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: days, labels: 'date' })
    expect(container.querySelector('rect[data-bar] title').textContent).toContain('28 Oct 10:00')
  })

  it('draws a bar for every hour', () => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: days, labels: 'date' })
    expect(container.querySelectorAll('rect[data-bar]')).toHaveLength(72)
  })
})

describe('BarChart without traffic', () => {
  it.each([
    ['no buckets', []],
    ['only empty buckets', [hour(8, 0), hour(9, 0)]],
  ])('says so instead of drawing a flat chart for %s', (_name, none) => {
    const { container } = render(BarChart, { title: 'Requests per hour', buckets: none })
    expect(screen.getByText('No traffic in this period')).toBeTruthy()
    expect(container.querySelector('svg')).toBeNull()
  })
})
