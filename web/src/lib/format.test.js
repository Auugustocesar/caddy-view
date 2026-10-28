import { describe, expect, it } from 'vitest'
import { formatBytes, formatClock, formatCount, formatDay, formatDuration, formatMs, formatPercent, formatStamp } from './format.js'

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

describe('formatDay and formatStamp', () => {
  it('say the local day and short month', () => {
    expect(formatDay(new Date(2026, 9, 28, 14, 5))).toBe('28 Oct')
    expect(formatDay(new Date(2026, 0, 3, 0, 0))).toBe('3 Jan')
  })

  it('add the time of day to the stamp', () => {
    expect(formatStamp(new Date(2026, 9, 28, 14, 5))).toBe('28 Oct 14:05')
    expect(formatStamp(new Date(2026, 11, 31, 0, 0))).toBe('31 Dec 00:00')
  })

  it('read an ISO timestamp', () => {
    const when = new Date(2026, 9, 28, 22, 30)
    expect(formatDay(when.toISOString())).toBe('28 Oct')
    expect(formatStamp(when.toISOString())).toBe('28 Oct 22:30')
  })
})

describe('formatDuration', () => {
  it.each([
    [0, 'for ever'],
    [86400, '1 day'],
    [14 * 86400, '14 days'],
    [36 * 3600, '36 hours'],
    [3600, '1 hour'],
    [90 * 60, '90 minutes'],
    [45, '45 seconds'],
  ])('shows %s seconds as %s', (seconds, want) => expect(formatDuration(seconds)).toBe(want))
})
