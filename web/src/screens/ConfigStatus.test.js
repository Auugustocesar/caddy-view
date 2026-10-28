import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ConfigView from './ConfigView.svelte'

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const system = {
  caddy: { reachable: true, endpoint: 'http://localhost:2019' },
  editing: {
    enabled: true,
    caddyfile: '/etc/caddy/Caddyfile',
    backupMode: 'sites-only',
    managedFile: '/etc/caddy/conf.d/caddy-view-sites.caddy',
    imported: true,
    drift: { known: false, drifted: false, revision: 0, running: 'matches' },
  },
  ingestion: { state: 'following', file: '/var/log/caddy/access.log', offset: 10, skippedLines: 2, gaps: 1, newestEntry: '2026-10-28T10:00:00Z' },
  scraper: { state: 'failing', error: 'metrics are not enabled in Caddy' },
  database: { kind: 'sqlite', file: '/data/history.db', sizeBytes: 2048, schemaVersion: 6 },
  retention: { rawSeconds: 14 * 86400, rollupsSeconds: 90 * 86400, maxBytes: 1073741824 },
}

function serve(body = system, status = 200) {
  const fetchMock = vi.fn(() => json(body, status))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const card = (name) => screen.findByRole('region', { name })

afterEach(() => vi.unstubAllGlobals())

describe('ConfigView status', () => {
  it('reads /api/system once when it opens', async () => {
    const fetchMock = serve()
    render(ConfigView, { config: {} })
    await card('Caddy')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/system')
  })

  it('shows Caddy and where it is reached', async () => {
    serve()
    render(ConfigView, { config: {} })
    const caddy = await card('Caddy')
    expect(caddy.textContent).toMatch(/reachable/i)
    expect(caddy.textContent).toContain('http://localhost:2019')
  })

  it('says Caddy cannot be reached, with the reason', async () => {
    serve({ ...system, caddy: { reachable: false, endpoint: 'http://localhost:2019', error: 'connection refused' } })
    render(ConfigView, { config: {} })
    const caddy = await card('Caddy')
    expect(caddy.textContent).toMatch(/not reachable/i)
    expect(caddy.textContent).toContain('connection refused')
  })

  it('shows editing: the file, the mode in words, the managed file and whether it is imported', async () => {
    serve()
    render(ConfigView, { config: {} })
    const editing = await card('Editing')
    expect(editing.textContent).toContain('/etc/caddy/Caddyfile')
    expect(editing.textContent).toMatch(/sites only/i)
    expect(editing.textContent).toContain('/etc/caddy/conf.d/caddy-view-sites.caddy')
    expect(editing.textContent).toMatch(/imported/i)
    expect(editing.textContent).toMatch(/Caddy runs it/i)
  })

  it('says editing is off, and how to turn it on', async () => {
    serve({ caddy: system.caddy, editing: { enabled: false } })
    render(ConfigView, { config: {} })
    const editing = await card('Editing')
    expect(editing.textContent).toMatch(/off/i)
    expect(editing.textContent).toContain('--allow-edit')
  })

  it('warns when the Caddyfile was edited outside caddy-view (full mode)', async () => {
    serve({ ...system, editing: { ...system.editing, backupMode: 'full', drift: { known: true, drifted: true, revision: 7, running: 'differs' } } })
    render(ConfigView, { config: {} })
    const editing = await card('Editing')
    expect(editing.textContent).toMatch(/full/i)
    expect(editing.textContent).toMatch(/edited outside/i)
    expect(editing.textContent).toMatch(/does not run/i)
  })

  it('shows the access log, the scraper, the database and the retention', async () => {
    serve()
    render(ConfigView, { config: {} })
    const log = await card('Access log')
    expect(log.textContent).toContain('/var/log/caddy/access.log')
    expect(log.textContent).toMatch(/following/i)
    expect(log.textContent).toMatch(/skipped/i)
    const scraper = await card('Metrics scraper')
    expect(scraper.textContent).toContain('metrics are not enabled in Caddy')
    const db = await card('Database')
    expect(db.textContent).toContain('/data/history.db')
    expect(db.textContent).toMatch(/2\.0 KB/)
    expect(db.textContent).toContain('6')
    const retention = await card('Retention')
    expect(retention.textContent).toMatch(/14 days/)
    expect(retention.textContent).toMatch(/90 days/)
  })

  it('leaves out a section the answer does not have', async () => {
    serve({ caddy: system.caddy, editing: { enabled: false } })
    render(ConfigView, { config: {} })
    await card('Caddy')
    for (const name of ['Access log', 'Metrics scraper', 'Database', 'Retention']) {
      expect(screen.queryByRole('region', { name })).toBeNull()
    }
  })

  it('shows the error of a section that has one', async () => {
    serve({ ...system, database: { error: 'the database could not be read' } })
    render(ConfigView, { config: {} })
    expect((await card('Database')).textContent).toContain('the database could not be read')
  })

  it('refreshes on demand', async () => {
    const fetchMock = serve()
    render(ConfigView, { config: {} })
    await card('Caddy')
    await fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
  })

  it('says the status could not be read without taking over the page', async () => {
    serve({ error: 'internal error' }, 500)
    render(ConfigView, { config: { a: 1 } })
    const note = await screen.findByText(/status could not be read/i)
    expect(note.closest('[role="alert"]')).toBeNull()
    expect(screen.getByTestId('config-json')).toBeTruthy()
  })

  it('renders what the status says as text', async () => {
    serve({ ...system, caddy: { reachable: true, endpoint: '<img src=x onerror=alert(1)>' } })
    const { container } = render(ConfigView, { config: {} })
    await card('Caddy')
    expect(container.querySelector('img')).toBeNull()
  })
})
