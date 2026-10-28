import { render, screen, within } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import Tile from './Tile.svelte'

describe('Tile', () => {
  it('shows its label, value and detail as one named group', () => {
    render(Tile, { label: 'Requests', value: '6,811', detail: '3 in flight' })
    const tile = screen.getByRole('group', { name: 'Requests' })
    expect(within(tile).getByText('6,811')).toBeTruthy()
    expect(within(tile).getByText('3 in flight')).toBeTruthy()
  })

  it('has no detail line when it has none', () => {
    render(Tile, { label: 'Traffic', value: '29.4 MB' })
    expect(screen.getByRole('group', { name: 'Traffic' }).textContent).toBe('Traffic29.4 MB')
  })

  it('carries its tone so it can be coloured', () => {
    render(Tile, { label: 'Server errors', value: '3 %', tone: 'bad' })
    expect(screen.getByRole('group', { name: 'Server errors' }).dataset.tone).toBe('bad')
  })

  it('has no tone attribute by default', () => {
    render(Tile, { label: 'Traffic', value: '1 B' })
    expect(screen.getByRole('group', { name: 'Traffic' }).hasAttribute('data-tone')).toBe(false)
  })

  it('renders hostile text as text', () => {
    const { container } = render(Tile, { label: 'L', value: '<img src=x onerror=alert(1)>' })
    expect(container.querySelector('img')).toBeNull()
  })
})
