import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import Segmented from './Segmented.svelte'

const options = [
  { value: 'live', label: 'Live' },
  { value: 'history', label: 'History' },
  { value: 'other', label: 'Other' },
]

const open = (props = {}) => {
  const onchange = vi.fn()
  const view = render(Segmented, { label: 'Logs view', options, value: 'live', onchange, ...props })
  return { ...view, onchange }
}

describe('Segmented', () => {
  it('is a group of radios named by its label, one for each option', () => {
    open()
    const group = screen.getByRole('radiogroup', { name: 'Logs view' })
    const radios = screen.getAllByRole('radio')
    expect(radios.map((r) => r.textContent.trim())).toEqual(['Live', 'History', 'Other'])
    for (const radio of radios) expect(group.contains(radio)).toBe(true)
  })

  it('marks the chosen option, and only that one', () => {
    open({ value: 'history' })
    expect(screen.getAllByRole('radio').map((r) => r.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false'])
  })

  it('asks for the option that was clicked', async () => {
    const { onchange } = open()
    await fireEvent.click(screen.getByRole('radio', { name: 'History' }))
    expect(onchange).toHaveBeenCalledExactlyOnceWith('history')
  })

  it('says nothing when the chosen option is clicked again', async () => {
    const { onchange } = open()
    await fireEvent.click(screen.getByRole('radio', { name: 'Live' }))
    expect(onchange).not.toHaveBeenCalled()
  })

  it('follows the value it is given', async () => {
    const { rerender } = open()
    await rerender({ label: 'Logs view', options, value: 'other' })
    expect(screen.getByRole('radio', { name: 'Other' }).getAttribute('aria-checked')).toBe('true')
  })

  it('can be reached with the Tab key at the chosen option only', () => {
    open({ value: 'history' })
    expect(screen.getAllByRole('radio').map((r) => r.tabIndex)).toEqual([-1, 0, -1])
  })

  it('moves with the arrow keys, around the ends', async () => {
    const { onchange } = open()
    await fireEvent.keyDown(screen.getByRole('radio', { name: 'Live' }), { key: 'ArrowRight' })
    expect(onchange).toHaveBeenLastCalledWith('history')
    await fireEvent.keyDown(screen.getByRole('radio', { name: 'Live' }), { key: 'ArrowLeft' })
    expect(onchange).toHaveBeenLastCalledWith('other')
  })

  it('ignores the keys it does not use', async () => {
    const { onchange } = open()
    await fireEvent.keyDown(screen.getByRole('radio', { name: 'Live' }), { key: 'a' })
    expect(onchange).not.toHaveBeenCalled()
  })

  it('renders hostile labels as text', () => {
    const { container } = open({ options: [{ value: 'x', label: '<img src=x onerror=alert(1)>' }], value: 'x' })
    expect(container.querySelector('img')).toBeNull()
  })
})
