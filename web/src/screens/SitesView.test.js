import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SitesView from './SitesView.svelte'

const A = 'a'.repeat(32)
const B = 'b'.repeat(32)
const MANAGED_FILE = '/etc/caddy/conf.d/caddy-view-sites.caddy'

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const siteA = {
  id: A,
  hostname: 'a.example.com',
  kind: 'proxy',
  upstreams: ['10.0.0.5:8080'],
  skipTlsVerify: false,
  tls: 'automatic',
  redirectTarget: '',
  redirectCode: 0,
  accessLog: false,
  enabled: true,
}
const siteB = {
  id: B,
  hostname: 'b.example.com',
  kind: 'redirect',
  upstreams: [],
  skipTlsVerify: false,
  tls: 'off',
  redirectTarget: 'https://x.example.com{uri}',
  redirectCode: 308,
  accessLog: false,
  enabled: false,
}

const listing = (extra = {}) => ({ sites: [siteA, siteB], managedFile: MANAGED_FILE, imported: true, accessLog: true, ...extra })
const running = {
  upstreamsAvailable: true,
  sites: [
    { server: 'srv0', host: 'a.example.com', upstreams: [{ address: '10.0.0.5:8080', numRequests: 0, fails: 0 }] },
    { server: 'srv0', host: 'hand.example.com', upstreams: [] },
  ],
}

// api builds the fake server. `routes` overrides an answer: a function of the request body that returns a response.
function api(routes = {}) {
  const calls = []
  const defaults = {
    'GET /api/managed-sites': () => json(listing()),
    'GET /api/sites': () => json(running),
    'POST /api/managed-sites/preview': () => json({ valid: true, text: '# generated\n' }),
    'POST /api/managed-sites': (body) => json({ site: { ...body, id: 'c'.repeat(32) }, revision: { number: 11 } }),
    [`PUT /api/managed-sites/${A}`]: (body) => json({ site: { ...body, id: A }, revision: { number: 12 } }),
    [`PUT /api/managed-sites/${B}`]: (body) => json({ site: { ...body, id: B }, revision: { number: 12 } }),
    [`DELETE /api/managed-sites/${A}`]: () => json({ revision: { number: 13 } }),
    'POST /api/managed-sites/import': () => json({ revision: { number: 10 } }),
  }
  vi.stubGlobal(
    'fetch',
    vi.fn((url, init = {}) => {
      const method = init.method ?? 'GET'
      const body = init.body ? JSON.parse(init.body) : undefined
      calls.push({ method, url, body })
      const handler = routes[`${method} ${url}`] ?? defaults[`${method} ${url}`]
      return handler ? handler(body) : Promise.reject(new Error(`unexpected request ${method} ${url}`))
    }),
  )
  return {
    calls,
    to: (method, url) => calls.filter((c) => c.method === method && c.url === url),
  }
}

const open = async (routes) => {
  const server = api(routes)
  const view = render(SitesView, { previewDelay: 0 })
  await screen.findByRole('heading', { level: 1, name: 'Sites' })
  // Everything the screen reads has been read when nothing says it is loading.
  await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull())
  return { ...view, server }
}

const managed = () => screen.getByRole('table', { name: 'Managed sites' })
const rowOf = (host) => within(managed()).getByText(host).closest('tr')
const button = (name) => screen.getByRole('button', { name })
const status = () => screen.getByRole('status').textContent

afterEach(() => vi.unstubAllGlobals())

describe('SitesView listing', () => {
  it('lists the managed sites with what each does, its TLS and whether it is enabled', async () => {
    await open()
    await within(managed()).findByText('a.example.com')
    const a = rowOf('a.example.com')
    expect(a.textContent).toContain('Proxy to 10.0.0.5:8080')
    expect(a.textContent).toContain('automatic')
    expect(a.textContent).toMatch(/enabled/i)
    const b = rowOf('b.example.com')
    expect(b.textContent).toContain('Redirect to https://x.example.com{uri} (308)')
    expect(b.textContent).toContain('off')
    expect(b.textContent).toMatch(/disabled/i)
  })

  it('marks the sites that ask for a password, and only those', async () => {
    const protectedSite = { ...siteA, basicAuth: { user: 'bob' } }
    await open({ 'GET /api/managed-sites': () => json(listing({ sites: [protectedSite, { ...siteB, basicAuth: null }] })) })
    await within(managed()).findByText('a.example.com')
    expect(rowOf('a.example.com').textContent).toMatch(/password/i)
    expect(rowOf('b.example.com').textContent).not.toMatch(/password/i)
  })

  it('names the user of a protected site, but never shows a password', async () => {
    const protectedSite = { ...siteA, basicAuth: { user: 'bob' } }
    await open({ 'GET /api/managed-sites': () => json(listing({ sites: [protectedSite] })) })
    await within(managed()).findByText('a.example.com')
    expect(rowOf('a.example.com').textContent).toContain('bob')
    expect(document.body.innerHTML).not.toMatch(/\$2[aby]\$/)
  })

  it('opens the wizard of a protected site with the password switch on', async () => {
    const protectedSite = { ...siteA, basicAuth: { user: 'bob' } }
    await open({ 'GET /api/managed-sites': () => json(listing({ sites: [protectedSite] })) })
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Edit a.example.com'))
    expect(screen.getByLabelText('Require a password').checked).toBe(true)
    expect(screen.getByLabelText('Username').value).toBe('bob')
  })

  it('says there are no managed sites yet', async () => {
    await open({ 'GET /api/managed-sites': () => json(listing({ sites: [] })) })
    expect(await screen.findByText(/no managed sites yet/i)).toBeTruthy()
    expect(screen.queryByRole('table', { name: 'Managed sites' })).toBeNull()
    expect(button('Add site').disabled).toBe(false)
  })

  it('shows what Caddy is running, marking the sites caddy-view manages', async () => {
    await open()
    const section = await screen.findByRole('region', { name: 'Running in Caddy' })
    const managedRow = within(section).getByText('a.example.com').closest('tr')
    const handRow = within(section).getByText('hand.example.com').closest('tr')
    expect(managedRow.textContent).toMatch(/managed/i)
    expect(handRow.textContent).toMatch(/caddyfile/i)
  })

  it('matches a running site to a managed one whatever the case', async () => {
    await open({ 'GET /api/sites': () => json({ ...running, sites: [{ server: 's', host: 'A.Example.COM', upstreams: [] }] }) })
    const section = await screen.findByRole('region', { name: 'Running in Caddy' })
    expect(section.textContent).toMatch(/managed/i)
  })

  it('does not call a disabled site managed-and-running', async () => {
    await open({ 'GET /api/sites': () => json({ ...running, sites: [{ server: 's', host: 'b.example.com', upstreams: [] }] }) })
    const section = await screen.findByRole('region', { name: 'Running in Caddy' })
    expect(within(section).getByText('b.example.com').closest('tr').textContent).toMatch(/caddyfile/i)
  })

  it('says so when it cannot read what Caddy runs, and still lists the managed sites', async () => {
    await open({ 'GET /api/sites': () => json({ error: 'caddy is not reachable' }, 502) })
    expect(await screen.findByText(/caddy is not reachable/i)).toBeTruthy()
    expect(within(managed()).getByText('a.example.com')).toBeTruthy()
  })

  it('shows a catch-all running site as any host', async () => {
    await open({ 'GET /api/sites': () => json({ ...running, sites: [{ server: 's', host: '', upstreams: [] }] }) })
    expect(await screen.findByText('(any host)')).toBeTruthy()
  })

  it('renders hostile names as text', async () => {
    const hostile = { ...siteA, hostname: '<img src=x onerror=alert(1)>' }
    const { container } = await open({ 'GET /api/managed-sites': () => json(listing({ sites: [hostile] })) })
    await screen.findByText('<img src=x onerror=alert(1)>')
    expect(container.querySelector('img')).toBeNull()
  })
})

describe('SitesView without editing', () => {
  it('says how to turn editing on, offers nothing to change, and still shows what Caddy runs', async () => {
    api({ 'GET /api/managed-sites': () => json({ error: 'not found' }, 404) })
    render(SitesView, { previewDelay: 0 })
    expect(await screen.findByText(/--allow-edit/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Add site' })).toBeNull()
    expect(await screen.findByText('hand.example.com')).toBeTruthy()
  })

  it('shows an error when caddy-view cannot be reached', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    render(SitesView, { previewDelay: 0 })
    expect((await screen.findByRole('alert')).textContent).toMatch(/caddy-view/)
  })

  it('shows an error for a failure that is not about editing being off', async () => {
    api({ 'GET /api/managed-sites': () => json({ error: 'internal error' }, 500) })
    render(SitesView, { previewDelay: 0 })
    expect((await screen.findByRole('alert')).textContent).toContain('internal error')
  })
})

describe('SitesView when the Caddyfile does not import the managed file', () => {
  const notImported = { 'GET /api/managed-sites': () => json(listing({ imported: false, sites: [] })) }

  it('says so, shows the file and offers to add the import', async () => {
    await open(notImported)
    const setup = await screen.findByRole('region', { name: 'Setup' })
    expect(setup.textContent).toMatch(/does not import/i)
    expect(within(setup).getByText(MANAGED_FILE)).toBeTruthy()
    expect(within(setup).getByRole('button', { name: 'Add the import' })).toBeTruthy()
  })

  it('waits for the import before a site can be added', async () => {
    await open(notImported)
    await screen.findByRole('region', { name: 'Setup' })
    expect(button('Add site').disabled).toBe(true)
  })

  it('says nothing about it when the Caddyfile imports the file', async () => {
    await open()
    await within(managed()).findByText('a.example.com')
    expect(screen.queryByRole('region', { name: 'Setup' })).toBeNull()
  })

  it('adds the import, then reads the state again and says it was done', async () => {
    let reads = 0
    const { server } = await open({
      'GET /api/managed-sites': () => json(++reads === 1 ? listing({ imported: false, sites: [] }) : listing({ sites: [] })),
    })
    await fireEvent.click(await screen.findByRole('button', { name: 'Add the import' }))

    await waitFor(() => expect(server.to('POST', '/api/managed-sites/import')).toHaveLength(1))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Setup' })).toBeNull())
    expect(status()).toMatch(/import/i)
    expect(status()).toContain('revision 10')
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
  })

  it('says why the import could not be added, and keeps offering it', async () => {
    await open({
      ...notImported,
      'POST /api/managed-sites/import': () => json({ error: 'Nothing was changed: caddy refused it', code: 'invalid' }, 422),
    })
    await fireEvent.click(await screen.findByRole('button', { name: 'Add the import' }))
    expect((await screen.findByRole('alert')).textContent).toContain('caddy refused it')
    expect(screen.getByRole('button', { name: 'Add the import' })).toBeTruthy()
  })

  it('does not post twice while it waits', async () => {
    let finish
    const { server } = await open({ ...notImported, 'POST /api/managed-sites/import': () => new Promise((r) => (finish = r)) })
    const add = await screen.findByRole('button', { name: 'Add the import' })
    await fireEvent.click(add)
    expect(add.disabled).toBe(true)
    await fireEvent.click(add)
    expect(server.to('POST', '/api/managed-sites/import')).toHaveLength(1)
    finish(new Response(JSON.stringify({ revision: { number: 10 } }), { status: 200 }))
  })
})

describe('SitesView adding and changing sites', () => {
  it('opens the wizard for a new site, and closes it on cancel', async () => {
    await open()
    await fireEvent.click(await screen.findByRole('button', { name: 'Add site' }))
    expect(screen.getByRole('heading', { name: 'Add a site' })).toBeTruthy()
    await fireEvent.click(button('Cancel'))
    expect(screen.queryByRole('heading', { name: 'Add a site' })).toBeNull()
  })

  it('tells the wizard whether an access log is known', async () => {
    await open({ 'GET /api/managed-sites': () => json(listing({ accessLog: false })) })
    await fireEvent.click(await screen.findByRole('button', { name: 'Add site' }))
    expect(screen.getByLabelText('Log requests').disabled).toBe(true)
  })

  it('adds the site, closes the wizard, reads the list again and says what happened', async () => {
    let reads = 0
    const added = { ...siteA, id: 'c'.repeat(32), hostname: 'c.example.com' }
    await open({ 'GET /api/managed-sites': () => json(++reads === 1 ? listing() : listing({ sites: [siteA, siteB, added] })) })
    await fireEvent.click(await screen.findByRole('button', { name: 'Add site' }))
    await fireEvent.input(screen.getByLabelText('Hostname'), { target: { value: 'c.example.com' } })
    await fireEvent.input(screen.getByLabelText('Upstreams'), { target: { value: '10.0.0.7:9000' } })
    const submit = await waitFor(() => {
      const b = screen.getAllByRole('button', { name: 'Add site' }).find((el) => el.closest('form') && !el.disabled)
      expect(b).toBeTruthy()
      return b
    })
    await fireEvent.click(submit)

    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Add a site' })).toBeNull())
    expect(await within(managed()).findByText('c.example.com')).toBeTruthy()
    expect(status()).toContain('c.example.com')
    expect(status()).toContain('revision 11')
  })

  it('opens the wizard on a site to change it', async () => {
    await open()
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Edit a.example.com'))
    expect(screen.getByRole('heading', { name: 'Edit a.example.com' })).toBeTruthy()
    expect(screen.getByLabelText('Upstreams').value).toBe('10.0.0.5:8080')
  })

  it('saves a change, closes the wizard and says what happened', async () => {
    const { server } = await open()
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Edit a.example.com'))
    await waitFor(() => expect(button('Save changes').disabled).toBe(false))
    await fireEvent.input(screen.getByLabelText('Upstreams'), { target: { value: '10.0.0.9:8080' } })
    await waitFor(() => expect(server.to('POST', '/api/managed-sites/preview').at(-1).body.upstreams).toEqual(['10.0.0.9:8080']))
    await fireEvent.click(button('Save changes'))

    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Edit a.example.com' })).toBeNull())
    expect(server.to('PUT', `/api/managed-sites/${A}`)[0].body.upstreams).toEqual(['10.0.0.9:8080'])
    expect(status()).toContain('revision 12')
  })

  it('shows one wizard at a time', async () => {
    await open()
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Add site'))
    await fireEvent.click(button('Edit b.example.com'))
    expect(screen.getAllByRole('form')).toHaveLength(1)
    expect(screen.getByRole('heading', { name: 'Edit b.example.com' })).toBeTruthy()
  })
})

describe('SitesView enabling, disabling and deleting', () => {
  it('disables an enabled site by putting it back with enabled off', async () => {
    const { server } = await open()
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Disable a.example.com'))

    await waitFor(() => expect(server.to('PUT', `/api/managed-sites/${A}`)).toHaveLength(1))
    expect(server.to('PUT', `/api/managed-sites/${A}`)[0].body).toEqual({
      hostname: 'a.example.com',
      kind: 'proxy',
      upstreams: ['10.0.0.5:8080'],
      skipTlsVerify: false,
      tls: 'automatic',
      redirectTarget: '',
      redirectCode: 0,
      accessLog: false,
      enabled: false,
      basicAuth: null,
    })
    await waitFor(() => expect(status()).toMatch(/disabled a\.example\.com/i))
  })

  it('keeps the password of a site when it is disabled and enabled again', async () => {
    const protectedSite = { ...siteA, basicAuth: { user: 'bob' } }
    const { server } = await open({ 'GET /api/managed-sites': () => json(listing({ sites: [protectedSite] })) })
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Disable a.example.com'))
    await waitFor(() => expect(server.to('PUT', `/api/managed-sites/${A}`)).toHaveLength(1))
    expect(server.to('PUT', `/api/managed-sites/${A}`)[0].body.basicAuth).toEqual({ user: 'bob', password: '' })
  })

  it('enables a disabled site, keeping its redirect', async () => {
    const { server } = await open()
    await within(managed()).findByText('b.example.com')
    await fireEvent.click(button('Enable b.example.com'))
    await waitFor(() => expect(server.to('PUT', `/api/managed-sites/${B}`)).toHaveLength(1))
    expect(server.to('PUT', `/api/managed-sites/${B}`)[0].body).toMatchObject({
      kind: 'redirect',
      redirectTarget: 'https://x.example.com{uri}',
      redirectCode: 308,
      upstreams: [],
      enabled: true,
    })
    await waitFor(() => expect(status()).toMatch(/enabled b\.example\.com/i))
  })

  it('says why a change of state was refused, and leaves the list as it was', async () => {
    await open({ [`PUT /api/managed-sites/${A}`]: () => json({ error: 'Nothing was applied: caddy refused the load', code: 'apply-failed' }, 502) })
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Disable a.example.com'))
    expect((await screen.findByRole('alert')).textContent).toContain('caddy refused the load')
    expect(rowOf('a.example.com').textContent).toMatch(/enabled/i)
  })

  it('asks before it deletes, and does nothing if told not to', async () => {
    const { server } = await open()
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Delete a.example.com'))
    expect(screen.getByText(/delete a\.example\.com\?/i)).toBeTruthy()
    expect(server.to('DELETE', `/api/managed-sites/${A}`)).toHaveLength(0)

    await fireEvent.click(button('Cancel'))
    expect(screen.queryByText(/delete a\.example\.com\?/i)).toBeNull()
    expect(server.to('DELETE', `/api/managed-sites/${A}`)).toHaveLength(0)
  })

  it('deletes when confirmed, reads the list again and says what happened', async () => {
    let reads = 0
    const { server } = await open({ 'GET /api/managed-sites': () => json(++reads === 1 ? listing() : listing({ sites: [siteB] })) })
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Delete a.example.com'))
    await fireEvent.click(button('Confirm delete'))

    await waitFor(() => expect(server.to('DELETE', `/api/managed-sites/${A}`)).toHaveLength(1))
    await waitFor(() => expect(within(managed()).queryByText('a.example.com')).toBeNull())
    expect(status()).toContain('a.example.com')
    expect(status()).toContain('revision 13')
  })

  it('says why a delete failed and keeps the site', async () => {
    await open({ [`DELETE /api/managed-sites/${A}`]: () => json({ error: 'Nothing was applied: caddy refused the load', code: 'apply-failed' }, 502) })
    await within(managed()).findByText('a.example.com')
    await fireEvent.click(button('Delete a.example.com'))
    await fireEvent.click(button('Confirm delete'))
    expect((await screen.findByRole('alert')).textContent).toContain('caddy refused the load')
    expect(within(managed()).getByText('a.example.com')).toBeTruthy()
  })
})
