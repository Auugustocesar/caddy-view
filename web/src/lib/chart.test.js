import { describe, expect, it } from 'vitest'
import { niceMax } from './chart.js'

describe('niceMax', () => {
  it.each([
    [0, 1],
    [1, 1],
    [3, 5],
    [37, 50],
    [50, 50],
    [680, 1000],
    [1500, 2000],
    [6811, 10000],
    [0.4, 0.5],
  ])('tops the axis for %s at %s', (value, want) => expect(niceMax(value)).toBe(want))
})
