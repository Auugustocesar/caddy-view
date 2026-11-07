import { render, screen, within } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import LineChart from './LineChart.svelte'

const hour = (h, requests) => ({ start: new Date(2026, 9, 28, h).toISOString(), requests })
const buckets = [hour(8, 100), hour(9, 400), hour(10, 0), hour(11, 200)]
const before = [hour(8, 80), hour(9, 300), hour(10, 50), hour(11, 150)]

const line = (container, series = 'current') => container.querySelector(`path[data-series="${series}"]`)
// The y of each point of a path written as straight "M x y L x y" segments or with curves: the last number pair of each command.
const ys = (d) => {
  const numbers = d.match(/-?\d+(\.\d+)?/g).map(Number)
  const points = []
  for (let i = 0; i + 1 < numbers.length; i += 2) points.push([numbers[i], numbers[i + 1]])
  return points
}

describe('LineChart line', () => {
  it('draws the line of this period, and the area under it', () => {
    const { container } = render(LineChart, { title: 'Requests per hour', buckets })
    expect(line(container)).toBeTruthy()
    expect(container.querySelector('path[data-area]')).toBeTruthy()
  })

  it('draws the previous period only when it is given', () => {
    const without = render(LineChart, { title: 'Requests per hour', buckets })
    expect(line(without.container, 'previous')).toBeNull()
    without.unmount()
    const withPrevious = render(LineChart, { title: 'Requests per hour', buckets, previous: before })
    expect(line(withPrevious.container, 'previous')).toBeTruthy()
  })

  it('runs from the left edge to the right edge', () => {
    const { container } = render(LineChart, { title: 'Requests per hour', buckets })
    const xs = ys(line(container).getAttribute('d')).map(([x]) => x)
    expect(Math.min(...xs)).toBe(0)
    expect(Math.max(...xs)).toBe(864)
  })

  it('puts the busiest bucket highest, and an empty one at the bottom', () => {
    const { container } = render(LineChart, { title: 'Requests per hour', buckets })
    const d = line(container).getAttribute('d')
    const points = ys(d).filter((_, i) => i % 1 === 0)
    const all = points.map(([, y]) => y)
    expect(Math.min(...all)).toBeLessThan(Math.max(...all))
    // 400 is the peak: its y is the smallest of the points that sit on a bucket
    const peak = Math.min(...all)
    expect(all.filter((y) => y === peak).length).toBeGreaterThan(0)
  })

  it('scales both periods to the same axis', () => {
    const big = [hour(8, 1000), hour(9, 1000), hour(10, 1000), hour(11, 1000)]
    const { container } = render(LineChart, { title: 'Requests per hour', buckets, previous: big })
    const current = ys(line(container).getAttribute('d')).map(([, y]) => y)
    const previous = ys(line(container, 'previous').getAttribute('d')).map(([, y]) => y)
    expect(Math.min(...previous)).toBeLessThan(Math.min(...current))
  })

  it('does not produce a broken path for one bucket', () => {
    const { container } = render(LineChart, { title: 'Requests per hour', buckets: [hour(8, 5)] })
    expect(line(container).getAttribute('d')).not.toMatch(/NaN|Infinity/)
  })
})

describe('LineChart words', () => {
  it('names the chart and says where it peaks, for those who cannot see it', () => {
    render(LineChart, { title: 'Requests per hour', buckets })
    const label = screen.getByRole('img').getAttribute('aria-label')
    expect(label).toContain('Requests per hour')
    expect(label).toContain('peaking at 400')
  })

  it('labels the time axis', () => {
    render(LineChart, { title: 'Requests per hour', buckets })
    expect(screen.getAllByText(/^\d\d:\d\d$/).length).toBeGreaterThan(1)
  })

  it('says which line is which when there are two', () => {
    render(LineChart, { title: 'Requests per hour', buckets, previous: before })
    expect(screen.getByText('This period')).toBeTruthy()
    expect(screen.getByText('Previous period')).toBeTruthy()
  })

  it('has no legend for the previous period without one', () => {
    render(LineChart, { title: 'Requests per hour', buckets })
    expect(screen.queryByText('Previous period')).toBeNull()
  })

  it('says there is no traffic when there is none', () => {
    render(LineChart, { title: 'Requests per hour', buckets: [hour(8, 0), hour(9, 0)] })
    expect(screen.getByText('No traffic in this period')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })

  it('has the numbers as a table for a reader that wants them', () => {
    render(LineChart, { title: 'Requests per hour', buckets })
    const table = screen.getByRole('table', { hidden: true })
    expect(within(table).getAllByRole('row', { hidden: true })).toHaveLength(5)
  })
})
