import { EditorView } from '@codemirror/view'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CaddyfileView from './CaddyfileView.svelte'

const SHA = 'a'.repeat(64)
const NEW_SHA = 'b'.repeat(64)
const FILE = 'example.com {\n\trespond "hi"\n}\n'

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const revision = (number, extra = {}) => ({
  number,
  time: '2026-10-28T10:00:00Z',
  trigger: 'manual-edit',
  status: 'applied',
  sha256: 'c'.repeat(64),
  ...extra,
})

// api builds the fake server. `routes` overrides an answer: a function of the request body that returns a response.
function api(routes = {}) {
  const calls = []
  const defaults = {
    'GET /api/system': () => json({ editing: { enabled: true, caddyfile: '/etc/caddy/Caddyfile' } }),
    'GET /api/caddyfile': () => json({ content: FILE, sha256: SHA }),
    'GET /api/revisions?limit=20': () => json({ revisions: [], next: null }),
    'POST /api/caddyfile/validate': () => json({ valid: true, warnings: [] }),
    'POST /api/caddyfile/format': (body) => json({ content: body.content, changed: false }),
    'POST /api/caddyfile/apply': () => json({ revision: revision(8, { sha256: NEW_SHA }) }),
    'POST /api/revisions/7/rollback': () => json({ revision: revision(9, { trigger: 'rollback' }) }),
  }
  const fetchMock = vi.fn((url, init = {}) => {
    const method = init.method ?? 'GET'
    const body = init.body ? JSON.parse(init.body) : undefined
    calls.push({ method, url, body })
    const handler = routes[`${method} ${url}`] ?? defaults[`${method} ${url}`]
    if (!handler) return Promise.reject(new Error(`unexpected request ${method} ${url}`))
    return handler(body)
  })
  vi.stubGlobal('fetch', fetchMock)
  return { calls, post: (path) => calls.filter((c) => c.method === 'POST' && c.url === path) }
}

const open = async (routes) => {
  const server = api(routes)
  const view = render(CaddyfileView, { validateDelay: 0 })
  await screen.findByLabelText('Caddyfile editor')
  return { ...view, server }
}

const viewOf = (container) => EditorView.findFromDOM(container.querySelector('.cm-editor'))
const textOf = (container) => viewOf(container).state.doc.toString()
const type = (container, text) =>
  viewOf(container).dispatch({ changes: { from: 0, to: viewOf(container).state.doc.length, insert: text } })
const status = () => screen.getByRole('status').textContent
const button = (name) => screen.getByRole('button', { name })
const ok = (body) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

afterEach(() => vi.unstubAllGlobals())

describe('CaddyfileView when it cannot edit', () => {
  it('says how to turn editing on and shows no editor', async () => {
    api({ 'GET /api/system': () => json({ editing: { enabled: false } }) })
    const { container } = render(CaddyfileView, { validateDelay: 0 })
    expect(await screen.findByText(/--allow-edit/)).toBeTruthy()
    expect(container.querySelector('.cm-editor')).toBeNull()
  })

  it('shows an error when the Caddyfile cannot be read', async () => {
    api({ 'GET /api/caddyfile': () => json({ error: 'internal error' }, 500) })
    render(CaddyfileView, { validateDelay: 0 })
    expect((await screen.findByRole('alert')).textContent).toContain('internal error')
  })

  it('shows an error when caddy-view cannot be reached', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    render(CaddyfileView, { validateDelay: 0 })
    expect((await screen.findByRole('alert')).textContent).toMatch(/caddy-view/)
  })
})

describe('CaddyfileView editing', () => {
  it('opens with the Caddyfile in the editor, nothing to apply', async () => {
    const { container } = await open()
    expect(screen.getByRole('heading', { level: 1, name: 'Caddyfile' })).toBeTruthy()
    expect(textOf(container)).toBe(FILE)
    expect(status()).toMatch(/no changes/i)
    expect(button('Apply').disabled).toBe(true)
    expect(button('Discard changes').disabled).toBe(true)
  })

  it('checks an edit with Caddy and says it is valid', async () => {
    const { container, server } = await open()
    type(container, 'example.com {\n\trespond "bye"\n}\n')
    await waitFor(() => expect(status()).toMatch(/valid/i))
    expect(server.post('/api/caddyfile/validate').at(-1).body).toEqual({ content: 'example.com {\n\trespond "bye"\n}\n' })
    expect(button('Apply').disabled).toBe(false)
  })

  it('says it is checking while it waits for Caddy', async () => {
    let answer
    const { container } = await open({ 'POST /api/caddyfile/validate': () => new Promise((resolve) => (answer = resolve)) })
    type(container, 'changed')
    await waitFor(() => expect(status()).toMatch(/checking/i))
    expect(button('Apply').disabled).toBe(true)
    await waitFor(() => expect(answer).toBeTypeOf('function')) // the check goes out after the pause
    expect(status()).toMatch(/checking/i)
    answer(ok({ valid: true, warnings: [] }))
    await waitFor(() => expect(status()).toMatch(/valid/i))
  })

  it('says why an edit is invalid and marks its line', async () => {
    const { container } = await open({
      'POST /api/caddyfile/validate': () => json({ valid: false, error: 'unrecognized directive: nope', line: 2 }),
    })
    type(container, 'example.com {\n\tnope\n}\n')
    await waitFor(() => expect(status()).toBe('Line 2: unrecognized directive: nope'))
    expect(container.querySelector('.cm-error-line').textContent).toBe('\tnope')
    expect(button('Apply').disabled).toBe(true)
  })

  it('says why an edit is invalid when Caddy gives no line', async () => {
    const { container } = await open({ 'POST /api/caddyfile/validate': () => json({ valid: false, error: 'ambiguous site definition' }) })
    type(container, 'x')
    await waitFor(() => expect(status()).toBe('ambiguous site definition'))
    expect(container.querySelector('.cm-error-line')).toBeNull()
  })

  it('shows what Caddy warns about', async () => {
    const { container } = await open({
      'POST /api/caddyfile/validate': () => json({ valid: true, warnings: [{ message: 'Caddyfile input is not formatted', line: 3 }] }),
    })
    type(container, 'x')
    await waitFor(() => expect(screen.getByText(/not formatted/)).toBeTruthy())
    expect(button('Apply').disabled).toBe(false)
  })

  it('only the check of the latest text counts', async () => {
    const waiting = []
    const { container } = await open({
      'POST /api/caddyfile/validate': (body) => new Promise((resolve) => waiting.push({ content: body.content, resolve })),
    })
    type(container, 'first')
    await waitFor(() => expect(waiting).toHaveLength(1))
    type(container, 'second')
    await waitFor(() => expect(waiting).toHaveLength(2))

    waiting[1].resolve(ok({ valid: true, warnings: [] }))
    await waitFor(() => expect(status()).toMatch(/valid/i))
    waiting[0].resolve(ok({ valid: false, error: 'stale', line: 1 }))
    await new Promise((r) => setTimeout(r, 10))
    expect(status()).toMatch(/valid/i)
    expect(container.querySelector('.cm-error-line')).toBeNull()
  })

  it('does not take text that matches the file for a change', async () => {
    const { container } = await open()
    type(container, 'changed')
    type(container, FILE)
    await waitFor(() => expect(status()).toMatch(/no changes/i))
    expect(button('Apply').disabled).toBe(true)
  })

  it('discards the changes', async () => {
    const { container } = await open()
    type(container, 'changed')
    await fireEvent.click(button('Discard changes'))
    expect(textOf(container)).toBe(FILE)
    expect(status()).toMatch(/no changes/i)
  })
})

describe('CaddyfileView formatting', () => {
  it('replaces the text with the formatted one', async () => {
    const { container, server } = await open({
      'POST /api/caddyfile/format': () => json({ content: 'a {\n\tb\n}\n', changed: true }),
    })
    type(container, 'a{\nb\n}')
    await fireEvent.click(button('Format'))
    await waitFor(() => expect(textOf(container)).toBe('a {\n\tb\n}\n'))
    expect(server.post('/api/caddyfile/format')[0].body).toEqual({ content: 'a{\nb\n}' })
  })

  it('shows an error when the text cannot be formatted', async () => {
    const { container } = await open({ 'POST /api/caddyfile/format': () => json({ error: 'internal error' }, 500) })
    type(container, 'a{')
    await fireEvent.click(button('Format'))
    expect((await screen.findByRole('alert')).textContent).toContain('internal error')
    expect(textOf(container)).toBe('a{')
  })
})

describe('CaddyfileView applying', () => {
  const readsInTurn = (...answers) => {
    let n = 0
    return () => json(answers[Math.min(n++, answers.length - 1)])
  }

  it('sends the text with the digest it was read from, then reads the file again', async () => {
    const { container, server } = await open({
      'GET /api/caddyfile': readsInTurn({ content: FILE, sha256: SHA }, { content: 'new text\n', sha256: NEW_SHA }),
    })
    type(container, 'new text\n')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))

    await waitFor(() => expect(status()).toMatch(/applied as revision 8/i))
    expect(server.post('/api/caddyfile/apply')[0].body).toEqual({ content: 'new text\n', baseSha256: SHA })
    expect(textOf(container)).toBe('new text\n')
    expect(button('Apply').disabled).toBe(true)
  })

  it('applies the next change on top of the new digest', async () => {
    const { container, server } = await open({
      'GET /api/caddyfile': readsInTurn({ content: FILE, sha256: SHA }, { content: 'one\n', sha256: NEW_SHA }),
    })
    type(container, 'one\n')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))
    await waitFor(() => expect(status()).toMatch(/applied/i))
    type(container, 'two\n')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))
    await waitFor(() => expect(server.post('/api/caddyfile/apply')).toHaveLength(2))
    expect(server.post('/api/caddyfile/apply')[1].body.baseSha256).toBe(NEW_SHA)
  })

  it('keeps the text and says what Caddy refused, with the line', async () => {
    const { container } = await open({
      'POST /api/caddyfile/apply': () => json({ error: 'unrecognized directive: nope', code: 'invalid', line: 2 }, 422),
    })
    type(container, 'a {\n\tnope\n}\n')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))

    expect((await screen.findByRole('alert')).textContent).toContain('unrecognized directive: nope')
    expect(textOf(container)).toBe('a {\n\tnope\n}\n')
    expect(container.querySelector('.cm-error-line').textContent).toBe('\tnope')
  })

  it('says why the load into Caddy failed', async () => {
    const { container } = await open({
      'POST /api/caddyfile/apply': () => json({ error: 'caddy refused the load', code: 'apply-failed' }, 502),
    })
    type(container, 'x')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))
    expect((await screen.findByRole('alert')).textContent).toContain('caddy refused the load')
  })

  it('offers to reload when the file changed since it was opened', async () => {
    const { container } = await open({
      'GET /api/caddyfile': readsInTurn({ content: FILE, sha256: SHA }, { content: 'from disk\n', sha256: NEW_SHA }),
      'POST /api/caddyfile/apply': () => json({ error: 'the Caddyfile changed since it was read', code: 'conflict' }, 409),
    })
    type(container, 'mine\n')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))

    expect((await screen.findByRole('alert')).textContent).toMatch(/changed/i)
    expect(textOf(container)).toBe('mine\n')
    await fireEvent.click(button('Reload from disk'))
    await waitFor(() => expect(textOf(container)).toBe('from disk\n'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('does not apply twice while an apply is in flight', async () => {
    let finish
    const { container, server } = await open({
      'POST /api/caddyfile/apply': () => new Promise((resolve) => (finish = resolve)),
    })
    type(container, 'x')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))
    expect(button('Apply').disabled).toBe(true)
    await fireEvent.click(button('Apply'))
    expect(server.post('/api/caddyfile/apply')).toHaveLength(1)
    finish(ok({ revision: revision(8) }))
  })
})

describe('CaddyfileView changes', () => {
  it('shows what the edit adds and removes against the file', async () => {
    const { container } = await open()
    type(container, 'example.com {\n\trespond "bye"\n}\n')
    await fireEvent.click(button('Show changes'))

    const changes = screen.getByRole('region', { name: 'Changes' })
    expect(within(changes).getByText('respond "hi"', { exact: false }).closest('[data-diff]').dataset.diff).toBe('del')
    expect(within(changes).getByText('respond "bye"', { exact: false }).closest('[data-diff]').dataset.diff).toBe('add')
    expect(button('Hide changes')).toBeTruthy()
  })

  it('says there is nothing to show when there are no changes', async () => {
    await open()
    await fireEvent.click(button('Show changes'))
    expect(within(screen.getByRole('region', { name: 'Changes' })).getByText(/no changes/i)).toBeTruthy()
  })

  it('renders hostile text as text', async () => {
    const { container } = await open()
    type(container, '<img src=x onerror=alert(1)>')
    await fireEvent.click(button('Show changes'))
    expect(document.querySelector('img')).toBeNull()
  })
})

describe('CaddyfileView history', () => {
  const revisions = [
    revision(7, { trigger: 'manual-edit', time: '2026-10-28T10:00:00Z', sha256: 'd'.repeat(64) }),
    revision(6, { trigger: 'rollback', status: 'applied', time: '2026-10-27T09:00:00Z', sha256: SHA }),
    revision(5, { trigger: 'manual-edit', status: 'failed', error: 'caddy refused it', time: '2026-10-26T08:00:00Z' }),
  ]
  const withHistory = (extra = {}) => ({
    'GET /api/revisions?limit=20': () => json({ revisions, next: null }),
    ...extra,
  })

  it('lists the revisions, newest first, with how they came about and how they ended', async () => {
    await open(withHistory())
    const history = await screen.findByRole('region', { name: 'History' })
    const items = within(history).getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items[0].textContent).toContain('Revision 7')
    expect(items[0].textContent).toMatch(/manual edit/i)
    expect(items[1].textContent).toMatch(/rollback/i)
    expect(items[2].textContent).toMatch(/failed/i)
    expect(items[2].textContent).toContain('caddy refused it')
  })

  it('says there is no history yet', async () => {
    await open()
    expect(await screen.findByText(/no revisions yet/i)).toBeTruthy()
  })

  it('marks the revision that is the file now, which cannot be rolled back to', async () => {
    await open(withHistory())
    const history = await screen.findByRole('region', { name: 'History' })
    const items = within(history).getAllByRole('listitem')
    expect(items[1].textContent).toMatch(/current/i)
    expect(within(items[1]).queryByRole('button', { name: /roll back/i })).toBeNull()
    expect(within(items[0]).getByRole('button', { name: 'Roll back to revision 7' })).toBeTruthy()
  })

  it('offers no roll back to a revision that failed', async () => {
    await open(withHistory())
    const history = await screen.findByRole('region', { name: 'History' })
    expect(within(history).queryByRole('button', { name: 'Roll back to revision 5' })).toBeNull()
  })

  it('loads more when there are more', async () => {
    const more = [revision(4, { sha256: 'e'.repeat(64) })]
    const { server } = await open({
      'GET /api/revisions?limit=20': () => json({ revisions, next: 5 }),
      'GET /api/revisions?limit=20&before=5': () => json({ revisions: more, next: null }),
    })
    await fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('Revision 4')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
    expect(server.calls.some((c) => c.url === '/api/revisions?limit=20&before=5')).toBe(true)
  })

  it('shows what a revision changed against the file as it is now', async () => {
    await open(
      withHistory({
        'GET /api/revisions/7': () => json({ ...revisions[0], content: 'example.com {\n\trespond "old"\n}\n' }),
      }),
    )
    await fireEvent.click(await screen.findByRole('button', { name: 'Compare revision 7 with the file' }))
    const panel = await screen.findByRole('region', { name: 'Revision 7 against the file' })
    expect(within(panel).getByText('respond "old"', { exact: false }).closest('[data-diff]').dataset.diff).toBe('del')
    expect(within(panel).getByText('respond "hi"', { exact: false }).closest('[data-diff]').dataset.diff).toBe('add')
  })

  it('asks before it rolls back, and does nothing if told not to', async () => {
    const { server } = await open(withHistory())
    await fireEvent.click(await screen.findByRole('button', { name: 'Roll back to revision 7' }))
    expect(screen.getByText(/roll back to revision 7\?/i)).toBeTruthy()
    expect(server.post('/api/revisions/7/rollback')).toHaveLength(0)

    await fireEvent.click(button('Cancel'))
    expect(screen.queryByText(/roll back to revision 7\?/i)).toBeNull()
    expect(server.post('/api/revisions/7/rollback')).toHaveLength(0)
  })

  it('rolls back when confirmed, and reads the file and the history again', async () => {
    let reads = 0
    const { container, server } = await open(
      withHistory({
        'GET /api/caddyfile': () => json(++reads === 1 ? { content: FILE, sha256: SHA } : { content: 'rolled back\n', sha256: NEW_SHA }),
      }),
    )
    await fireEvent.click(await screen.findByRole('button', { name: 'Roll back to revision 7' }))
    await fireEvent.click(button('Confirm roll back'))

    await waitFor(() => expect(server.post('/api/revisions/7/rollback')).toHaveLength(1))
    await waitFor(() => expect(textOf(container)).toBe('rolled back\n'))
    expect(status()).toMatch(/revision 9/i)
    expect(server.calls.filter((c) => c.url === '/api/revisions?limit=20').length).toBeGreaterThanOrEqual(2)
  })

  it('says why a roll back failed and keeps the file as it was', async () => {
    const { container } = await open(
      withHistory({
        'POST /api/revisions/7/rollback': () => json({ error: 'caddy refused the load', code: 'apply-failed' }, 502),
      }),
    )
    await fireEvent.click(await screen.findByRole('button', { name: 'Roll back to revision 7' }))
    await fireEvent.click(button('Confirm roll back'))
    expect((await screen.findByRole('alert')).textContent).toContain('caddy refused the load')
    expect(textOf(container)).toBe(FILE)
  })

  it('warns that a roll back would throw away unsaved edits', async () => {
    const { container, server } = await open(withHistory())
    type(container, 'my edits')
    await fireEvent.click(await screen.findByRole('button', { name: 'Roll back to revision 7' }))
    expect(screen.getByText(/unsaved changes/i)).toBeTruthy()
    expect(server.post('/api/revisions/7/rollback')).toHaveLength(0)
  })
})

describe('CaddyfileView with what Caddy really says', () => {
  const verbose = "adapting config using caddyfile adapter: syntax error: unexpected token 'x', expecting '}', at Caddyfile:25 import chain: ['']"

  it('shows the check without the parts the page repeats', async () => {
    const { container } = await open({ 'POST /api/caddyfile/validate': () => json({ valid: false, error: verbose, line: 25 }) })
    type(container, 'x')
    await waitFor(() => expect(status()).toBe("Line 25: syntax error: unexpected token 'x', expecting '}'"))
  })

  it('shows a refused apply the same way', async () => {
    const { container } = await open({ 'POST /api/caddyfile/apply': () => json({ error: verbose, code: 'invalid', line: 25 }, 422) })
    type(container, 'x')
    await waitFor(() => expect(button('Apply').disabled).toBe(false))
    await fireEvent.click(button('Apply'))
    expect((await screen.findByRole('alert')).textContent.trim()).toBe("syntax error: unexpected token 'x', expecting '}'")
  })
})
