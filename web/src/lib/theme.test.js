import { describe, expect, it } from 'vitest'
import { THEME_KEY, applyTheme, loadTheme, saveTheme, toggledTheme } from './theme.js'

const storageOf = (items = {}) => ({
  getItem: (key) => (key in items ? items[key] : null),
  setItem: (key, value) => {
    items[key] = value
  },
})

const refusing = {
  getItem() {
    throw new Error('storage is blocked')
  },
  setItem() {
    throw new Error('storage is blocked')
  },
}

describe('loadTheme', () => {
  it.each([
    ['light', 'light'],
    ['dark', 'dark'],
    ['system', 'system'],
    [null, 'system'],
    ['purple', 'system'],
    ['', 'system'],
  ])('reads %s as %s', (stored, want) => {
    expect(loadTheme(storageOf(stored === null ? {} : { [THEME_KEY]: stored }))).toBe(want)
  })

  it('follows the system when storage cannot be read', () => {
    expect(loadTheme(refusing)).toBe('system')
  })
})

describe('saveTheme', () => {
  it('remembers the choice under the key loadTheme reads', () => {
    const items = {}
    saveTheme(storageOf(items), 'dark')
    expect(loadTheme(storageOf(items))).toBe('dark')
  })

  it('does not throw when storage refuses', () => {
    expect(() => saveTheme(refusing, 'dark')).not.toThrow()
  })
})

describe('applyTheme', () => {
  it('shows a chosen theme on the root element', () => {
    const root = document.createElement('html')
    applyTheme(root, 'dark')
    expect(root.dataset.theme).toBe('dark')
    applyTheme(root, 'light')
    expect(root.dataset.theme).toBe('light')
  })

  it('leaves the choice to the system by removing the attribute', () => {
    const root = document.createElement('html')
    applyTheme(root, 'dark')
    applyTheme(root, 'system')
    expect(root.hasAttribute('data-theme')).toBe(false)
  })
})

describe('toggledTheme', () => {
  it.each([
    ['light', false, 'dark'],
    ['dark', false, 'light'],
    ['system', false, 'dark'],
    ['system', true, 'light'],
    // A choice wins over what the system prefers.
    ['light', true, 'dark'],
    ['dark', true, 'light'],
  ])('from %s with a dark system %s goes to %s', (current, systemDark, want) => {
    expect(toggledTheme(current, systemDark)).toBe(want)
  })
})
