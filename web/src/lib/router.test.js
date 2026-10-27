import { describe, expect, it } from 'vitest'
import { TABS, tabFromPath } from './router.js'

describe('tabs', () => {
  it('lists the three phase 1 tabs in order, each with its own path', () => {
    expect(TABS.map((t) => [t.id, t.label, t.path])).toEqual([
      ['dashboard', 'Dashboard', '/'],
      ['config', 'Config', '/config'],
      ['logs', 'Logs', '/logs'],
    ])
  })
})

describe('tabFromPath', () => {
  it.each([
    ['/', 'dashboard'],
    ['', 'dashboard'],
    ['/config', 'config'],
    ['/config/', 'config'],
    ['/logs', 'logs'],
    ['/logs/', 'logs'],
    ['/nope', 'dashboard'],
    ['/logs/extra', 'dashboard'],
  ])('%j is %s', (path, want) => {
    expect(tabFromPath(path)).toBe(want)
  })
})
