/** @type {ReadonlyArray<{id: 'dashboard'|'config'|'logs', label: string, path: string}>} */
export const TABS = [
  { id: 'dashboard', label: 'Dashboard', path: '/' },
  { id: 'config', label: 'Config', path: '/config' },
  { id: 'logs', label: 'Logs', path: '/logs' },
]

/** The tab for a URL path. Unknown paths show the dashboard. @param {string} pathname @returns {'dashboard'|'config'|'logs'} */
export function tabFromPath(pathname) {
  const path = pathname.length > 1 ? pathname.replace(/\/$/, '') : pathname
  return TABS.find((t) => t.path === path)?.id ?? 'dashboard'
}
