import { describe, expect, it } from 'vitest'
import { formatBytes, formatClock, formatCount, formatMs, formatPercent } from './format.js'

describe('formatCount', () => {
  it.each([
    [0, '0'],
    [7, '7'],
    [6811, '6,811'],
    [1234567, '1,234,567'],
    [null, '–'],
    [undefined, '–'],
  ])('shows %s as %s', (n, want) => expect(formatCount(n)).toBe(want))
})

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [1023, '1023 B'],
    [1024, '1.0 KB'],
    [1536, '1.5 KB'],
    [30828134, '29.4 MB'],
    [5 * 1024 ** 3, '5.0 GB'],
    [3 * 1024 ** 4, '3.0 TB'],
    [null, '–'],
    [undefined, '–'],
  ])('shows %s as %s', (n, want) => expect(formatBytes(n)).toBe(want))
})

describe('formatPercent', () => {
  it.each([
    [0, '0 %'],
    [0.006, '0.6 %'],
    [0.02, '2 %'],
    [0.1234, '12.3 %'],
    [1, '100 %'],
    [null, '–'],
    [undefined, '–'],
  ])('shows %s as %s', (rate, want) => expect(formatPercent(rate)).toBe(want))

  it('does not round a rate that exists down to nothing', () => {
    expect(formatPercent(0.0001)).toBe('<0.1 %')
  })
})

describe('formatMs', () => {
  it.each([
    [0, '0 ms'],
    [0.4, '<1 ms'],
    [42.3, '42 ms'],
    [999.4, '999 ms'],
    [999.6, '1 s'],
    [1500, '1.5 s'],
    [30000, '30 s'],
    [null, '–'],
    [undefined, '–'],
  ])('shows %s as %s', (ms, want) => expect(formatMs(ms)).toBe(want))
})

describe('formatClock', () => {
  it('shows the local time of day with two digits each', () => {
    expect(formatClock(new Date(2026, 9, 28, 9, 5))).toBe('09:05')
    expect(formatClock(new Date(2026, 9, 28, 17, 0))).toBe('17:00')
  })

  it('reads an ISO timestamp', () => {
    const when = new Date(2026, 9, 28, 22, 30)
    expect(formatClock(when.toISOString())).toBe('22:30')
  })
})
