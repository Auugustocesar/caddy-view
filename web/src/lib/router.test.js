import { describe, expect, it } from 'vitest'
import { TABS, tabFromPath } from './router.js'

describe('tabs', () => {
  it('lists the tabs in order, each with its own path', () => {
    expect(TABS.map((t) => [t.id, t.label, t.path])).toEqual([
      ['dashboard', 'Dashboard', '/'],
      ['metrics', 'Metrics', '/metrics'],
      ['logs', 'Logs', '/logs'],
      ['sites', 'Sites', '/sites'],
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
    ['/metrics', 'metrics'],
    ['/metrics/', 'metrics'],
    ['/logs', 'logs'],
    ['/logs/', 'logs'],
    ['/sites', 'sites'],
    ['/sites/', 'sites'],
    ['/caddyfile', 'caddyfile'],
    ['/caddyfile/', 'caddyfile'],
    ['/nope', 'dashboard'],
    ['/logs/extra', 'dashboard'],
  ])('%j is %s', (path, want) => {
    expect(tabFromPath(path)).toBe(want)
  })
})
