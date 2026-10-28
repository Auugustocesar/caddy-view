import { EditorView } from '@codemirror/view'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CaddyfileView from './CaddyfileView.svelte'
import SitesView from './SitesView.svelte'

const SHA = 'a'.repeat(64)
const FILE = 'example.com {\n\trespond "hi"\n}\n'
const MANAGED_FILE = '/etc/caddy/conf.d/caddy-view-sites.caddy'

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const revision = (number, extra = {}) => ({
  number,
  time: '2026-10-28T10:00:00Z',
  trigger: 'site-change',
  status: 'applied',
  sha256: 'c'.repeat(64),
  ...extra,
})

// serve answers the routes of both tabs; `routes` overrides an answer.
function serve(routes = {}) {
  const calls = []
  const defaults = {
    'GET /api/system': () => json({ editing: { enabled: true, caddyfile: '/etc/caddy/Caddyfile', backupMode: 'sites-only' } }),
    'GET /api/caddyfile': () => json({ content: FILE, sha256: SHA }),
    'GET /api/revisions?limit=20': () => json({ revisions: [], next: null }),
    'GET /api/managed-sites': () => json({ sites: [], managedFile: MANAGED_FILE, imported: false, accessLog: false, backupMode: 'sites-only' }),
    'GET /api/sites': () => json({ upstreamsAvailable: true, sites: [] }),
    'POST /api/revisions/6/rollback': () => json({ revision: revision(8, { trigger: 'rollback' }) }),
    'POST /api/managed-sites/import': () => json({ revision: { number: 10 } }),
  }
  vi.stubGlobal(
    'fetch',
    vi.fn((url, init = {}) => {
      const method = init.method ?? 'GET'
      calls.push({ method, url })
      const handler = routes[`${method} ${url}`] ?? defaults[`${method} ${url}`]
      return handler ? handler() : Promise.reject(new Error(`unexpected request ${method} ${url}`))
    }),
  )
  return { calls, post: (path) => calls.filter((c) => c.method === 'POST' && c.url === path) }
}

const viewOf = (container) => EditorView.findFromDOM(container.querySelector('.cm-editor'))

afterEach(() => vi.unstubAllGlobals())

describe('CaddyfileView in the sites-only backup mode', () => {
  const open = async (routes) => {
    const server = serve(routes)
    const view = render(CaddyfileView, { validateDelay: 0 })
    await screen.findByLabelText('Caddyfile editor')
    return { ...view, server }
  }
  const history = {
    'GET /api/revisions?limit=20': () => json({ revisions: [revision(7), revision(6)], next: null }),
  }

  it('shows the file as it is, read-only, and says why', async () => {
    const { container } = await open()
    expect(viewOf(container).state.doc.toString()).toBe(FILE)
    expect(container.querySelector('.cm-content').getAttribute('contenteditable')).toBe('false')
    expect(screen.getByText(/never writes the Caddyfile/i)).toBeTruthy()
  })

  it('offers nothing that would change the Caddyfile', async () => {
    const { server } = await open()
    for (const name of ['Format', 'Apply', 'Discard changes', 'Show changes']) {
      expect(screen.queryByRole('button', { name })).toBeNull()
    }
    expect(server.calls.some((c) => c.method === 'POST')).toBe(false)
  })

  it('still lists the history and rolls back the managed sites', async () => {
    const { server } = await open(history)
    await screen.findByRole('button', { name: 'Roll back to revision 6' })
    expect(screen.queryByRole('button', { name: /Compare revision/ })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Roll back to revision 6' }))
    expect(screen.getByText(/managed sites/i)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm roll back' }))
    await waitFor(() => expect(server.post('/api/revisions/6/rollback')).toHaveLength(1))
  })

  it('stays editable in the full mode', async () => {
    const { container } = await open({
      'GET /api/system': () => json({ editing: { enabled: true, caddyfile: '/x', backupMode: 'full' } }),
    })
    expect(container.querySelector('.cm-content').getAttribute('contenteditable')).toBe('true')
    expect(screen.getByRole('button', { name: 'Apply' })).toBeTruthy()
  })
})

describe('SitesView in the sites-only backup mode', () => {
  it('does not offer to add the import: it says which line to add by hand', async () => {
    serve()
    render(SitesView, { previewDelay: 0 })
    const setup = await screen.findByRole('region', { name: 'Setup' })
    expect(within(setup).queryByRole('button', { name: 'Add the import' })).toBeNull()
    expect(setup.textContent).toContain(`import ${MANAGED_FILE}`)
  })

  it('still offers the import in the full mode', async () => {
    serve({
      'GET /api/managed-sites': () => json({ sites: [], managedFile: MANAGED_FILE, imported: false, accessLog: false, backupMode: 'full' }),
    })
    render(SitesView, { previewDelay: 0 })
    expect(await screen.findByRole('button', { name: 'Add the import' })).toBeTruthy()
  })
})
