import { describe, expect, it } from 'vitest'
import { TABS, tabFromPath } from './router.js'

describe('tabs', () => {
  it('lists the tabs in order, each with its own path', () => {
    expect(TABS.map((t) => [t.id, t.label, t.path])).toEqual([
      ['dashboard', 'Dashboard', '/'],
      ['logs', 'Logs', '/logs'],
      ['caddyfile', 'Caddyfile', '/caddyfile'],
      ['config', 'Config', '/config'],
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
    ['/caddyfile', 'caddyfile'],
    ['/caddyfile/', 'caddyfile'],
    ['/nope', 'dashboard'],
    ['/logs/extra', 'dashboard'],
  ])('%j is %s', (path, want) => {
    expect(tabFromPath(path)).toBe(want)
  })
})
