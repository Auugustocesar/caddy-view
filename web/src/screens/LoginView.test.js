import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '../lib/api.js'
import LoginView from './LoginView.svelte'

function setup(login) {
  const onlogin = vi.fn()
  render(LoginView, { login, onlogin })
  return { onlogin }
}

const username = () => screen.getByLabelText('Username')
const password = () => screen.getByLabelText('Password')
const submit = () => screen.getByRole('button', { name: 'Sign in' })

describe('LoginView', () => {
  it('asks for a username, starting with admin, and a password that is hidden', () => {
    setup(vi.fn())
    expect(username().value).toBe('admin')
    expect(password().type).toBe('password')
    expect(password().getAttribute('autocomplete')).toBe('current-password')
    expect(username().getAttribute('autocomplete')).toBe('username')
  })

  it('sends what was typed and hands the user over', async () => {
    const login = vi.fn().mockResolvedValue('admin')
    const { onlogin } = setup(login)
    await fireEvent.input(password(), { target: { value: 'hunter2' } })
    await fireEvent.click(submit())
    expect(login).toHaveBeenCalledWith('admin', 'hunter2')
    await waitFor(() => expect(onlogin).toHaveBeenCalledWith('admin'))
  })

  it('submits with Enter, which is a form submit', async () => {
    const login = vi.fn().mockResolvedValue('admin')
    setup(login)
    await fireEvent.input(password(), { target: { value: 'x' } })
    await fireEvent.submit(screen.getByRole('button', { name: 'Sign in' }).closest('form'))
    expect(login).toHaveBeenCalledOnce()
  })

  it('does not send an empty password', async () => {
    const login = vi.fn()
    setup(login)
    await fireEvent.click(submit())
    expect(login).not.toHaveBeenCalled()
  })

  it('shows the refusal as an alert, keeps the username, empties the password, and does not hand over', async () => {
    const login = vi.fn().mockRejectedValue(new ApiError('invalid username or password', 401))
    const { onlogin } = setup(login)
    await fireEvent.input(username(), { target: { value: 'bob' } })
    await fireEvent.input(password(), { target: { value: 'nope' } })
    await fireEvent.click(submit())
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('invalid username or password')
    expect(username().value).toBe('bob')
    expect(password().value).toBe('')
    expect(onlogin).not.toHaveBeenCalled()
  })

  it('says how long to wait when held back', async () => {
    const login = vi.fn().mockRejectedValue(new ApiError('too many failed logins', 429, { retryAfter: 90 }))
    setup(login)
    await fireEvent.input(password(), { target: { value: 'nope' } })
    await fireEvent.click(submit())
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toMatch(/too many/i)
    expect(alert.textContent).toMatch(/2 minutes|90 seconds/)
  })

  it('says it cannot reach caddy-view when the server does not answer', async () => {
    const login = vi.fn().mockRejectedValue(new ApiError('cannot reach caddy-view', 0))
    setup(login)
    await fireEvent.input(password(), { target: { value: 'x' } })
    await fireEvent.click(submit())
    expect((await screen.findByRole('alert')).textContent).toContain('cannot reach caddy-view')
  })

  it('cannot be submitted twice while waiting', async () => {
    let finish
    const login = vi.fn(() => new Promise((resolve) => (finish = resolve)))
    setup(login)
    await fireEvent.input(password(), { target: { value: 'x' } })
    await fireEvent.click(submit())
    expect(submit().disabled).toBe(true)
    await fireEvent.click(submit())
    expect(login).toHaveBeenCalledOnce()
    finish('admin')
    await waitFor(() => expect(submit().disabled).toBe(false))
  })
})
