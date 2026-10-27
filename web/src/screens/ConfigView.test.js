import { fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ConfigView from './ConfigView.svelte'

const config = { apps: { http: { servers: { srv0: { listen: [':443'] } } } }, secret: '[redacted]' }

afterEach(() => vi.unstubAllGlobals())

describe('ConfigView', () => {
  it('shows the configuration pretty printed', () => {
    render(ConfigView, { config })
    expect(screen.getByTestId('config-json').textContent).toBe(JSON.stringify(config, null, 2))
  })

  it('shows redacted values as they came', () => {
    render(ConfigView, { config })
    expect(screen.getByTestId('config-json').textContent).toContain('"secret": "[redacted]"')
  })

  it('shows a loading message before the configuration arrives', () => {
    render(ConfigView, { config: null })
    expect(screen.getByText('Loading…')).toBeTruthy()
    expect(screen.queryByTestId('config-json')).toBeNull()
  })

  it('shows the error when the configuration cannot be read', () => {
    render(ConfigView, { config: null, loadError: 'caddy is down' })
    expect(screen.getByRole('alert').textContent).toContain('caddy is down')
  })

  it('copies the text to the clipboard and says so', async () => {
    const writeText = vi.fn().mockResolvedValue()
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    render(ConfigView, { config })

    await fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith(JSON.stringify(config, null, 2))
    expect(await screen.findByText('Copied')).toBeTruthy()
  })

  it('says when the copy did not work', async () => {
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } })
    render(ConfigView, { config })

    await fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await screen.findByText('Copy failed')).toBeTruthy()
  })

  it('renders the configuration as text, never as markup', () => {
    const { container } = render(ConfigView, { config: { name: '<img src=x onerror=alert(1)>' } })
    expect(container.querySelector('img')).toBeNull()
  })
})
