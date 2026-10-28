/** @type {ReadonlyArray<{id: 'dashboard'|'logs'|'caddyfile'|'config', label: string, path: string}>} */
export const TABS = [
  { id: 'dashboard', label: 'Dashboard', path: '/' },
  { id: 'logs', label: 'Logs', path: '/logs' },
  { id: 'caddyfile', label: 'Caddyfile', path: '/caddyfile' },
  { id: 'config', label: 'Config', path: '/config' },
]

/** The tab for a URL path. Unknown paths show the dashboard. @param {string} pathname @returns {'dashboard'|'logs'|'caddyfile'|'config'} */
export function tabFromPath(pathname) {
  const path = pathname.length > 1 ? pathname.replace(/\/$/, '') : pathname
  return TABS.find((t) => t.path === path)?.id ?? 'dashboard'
}
