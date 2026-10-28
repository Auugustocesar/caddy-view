/** @typedef {'system'|'light'|'dark'} Theme */

export const THEME_KEY = 'caddy-view-theme'

/** The stored choice; `system` when there is none, it is not valid, or storage cannot be read. @param {Pick<Storage,'getItem'>} storage @returns {Theme} */
export function loadTheme(storage) {
  try {
    const stored = storage.getItem(THEME_KEY)
    return stored === 'light' || stored === 'dark' ? stored : 'system'
  } catch {
    // Storage is blocked (a private window, for example): follow the system.
    return 'system'
  }
}

/** Remembers the choice; a storage that refuses is ignored. @param {Pick<Storage,'setItem'>} storage @param {Theme} theme */
export function saveTheme(storage, theme) {
  try {
    storage.setItem(THEME_KEY, theme)
  } catch {
    // The choice still applies to this page; it is only not remembered.
  }
}

/** Shows the theme on the root element: `data-theme`, or none for `system`. @param {HTMLElement} root @param {Theme} theme */
export function applyTheme(root, theme) {
  if (theme === 'system') root.removeAttribute('data-theme')
  else root.dataset.theme = theme
}

/** The theme the button switches to: the opposite of what is shown now. @param {Theme} current @param {boolean} systemDark @returns {'light'|'dark'} */
export function toggledTheme(current, systemDark) {
  const shown = current === 'system' ? (systemDark ? 'dark' : 'light') : current
  return shown === 'dark' ? 'light' : 'dark'
}
