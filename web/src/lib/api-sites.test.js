import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, addImport, createSite, deleteSite, getManagedSites, previewSite, updateSite } from './api.js'

function respond(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const fetchOf = (body = {}, status = 200) => {
  const fetchMock = vi.fn().mockResolvedValue(respond(status, body))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const site = { hostname: 'app.example.com', kind: 'proxy', upstreams: ['10.0.0.5:8080'], tls: 'automatic' }
const ID = 'c'.repeat(32)

afterEach(() => vi.unstubAllGlobals())

describe('the managed sites calls', () => {
  it('getManagedSites reads the list', async () => {
    const fetchMock = fetchOf({ sites: [], managedFile: '/x', imported: true, accessLog: false })
    await expect(getManagedSites()).resolves.toEqual({ sites: [], managedFile: '/x', imported: true, accessLog: false })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/managed-sites')
    expect(fetchMock.mock.calls[0][1]?.method ?? 'GET').toBe('GET')
  })

  it.each([
    ['previewSite', () => previewSite(site), 'POST', '/api/managed-sites/preview', site],
    ['previewSite of a site that exists', () => previewSite(site, ID), 'POST', '/api/managed-sites/preview', { id: ID, ...site }],
    ['createSite', () => createSite(site), 'POST', '/api/managed-sites', site],
    ['updateSite', () => updateSite(ID, site), 'PUT', `/api/managed-sites/${ID}`, site],
    ['deleteSite', () => deleteSite(ID), 'DELETE', `/api/managed-sites/${ID}`, undefined],
    ['addImport', () => addImport(), 'POST', '/api/managed-sites/import', {}],
  ])('%s sends %s to %s', async (_name, call, method, path, body) => {
    const fetchMock = fetchOf({ ok: true })
    await call()
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe(path)
    expect(init.method).toBe(method)
    // The server wants JSON on every change, a delete included, whatever the body.
    expect(init.headers['Content-Type']).toBe('application/json')
    if (body === undefined) expect(init.body).toBeUndefined()
    else expect(JSON.parse(init.body)).toEqual(body)
  })

  it('keeps the errors by field of a refused site', async () => {
    const errors = [{ site: 'x', field: 'hostname', message: 'is required' }]
    fetchOf({ error: 'Nothing was applied: the site is not valid', code: 'invalid-site', errors }, 422)
    const err = await createSite(site).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(422)
    expect(err.code).toBe('invalid-site')
    expect(err.errors).toEqual(errors)
  })

  it('keeps the site Caddy rejected and its line', async () => {
    fetchOf({ error: 'caddy rejected the site a.example.com', code: 'site-rejected', site: { id: ID, hostname: 'a.example.com' }, line: 4 }, 422)
    const err = await updateSite(ID, site).catch((e) => e)
    expect(err.code).toBe('site-rejected')
    expect(err.site).toEqual({ id: ID, hostname: 'a.example.com' })
    expect(err.line).toBe(4)
  })

  it('has no errors or site on an error that came without them', async () => {
    fetchOf({ error: 'boom' }, 500)
    const err = await deleteSite(ID).catch((e) => e)
    expect(err.errors).toBeUndefined()
    expect(err.site).toBeUndefined()
  })

  it('answers 404 for a server without editing, so the screen can tell', async () => {
    fetchOf({ error: 'not found' }, 404)
    const err = await getManagedSites().catch((e) => e)
    expect(err.status).toBe(404)
  })
})
