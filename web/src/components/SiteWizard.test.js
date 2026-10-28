import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SiteWizard from './SiteWizard.svelte'

const ID = 'a'.repeat(32)

const json = (body, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
const ok = (body) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

const savedAnswer = { site: { id: ID, hostname: 'app.example.com' }, revision: { number: 5 } }

// api builds the fake server. `routes` overrides an answer: a function of the request body that returns a response.
function api(routes = {}) {
  const calls = []
  const defaults = {
    'POST /api/managed-sites/preview': () => json({ valid: true, text: '# generated\napp.example.com {\n}\n' }),
    'POST /api/managed-sites': () => json(savedAnswer),
    [`PUT /api/managed-sites/${ID}`]: () => json(savedAnswer),
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
  const to = (method, url) => calls.filter((c) => c.method === method && c.url === url)
  return { calls, previews: () => to('POST', '/api/managed-sites/preview'), creates: () => to('POST', '/api/managed-sites'), updates: () => to('PUT', `/api/managed-sites/${ID}`) }
}

const open = (props = {}, routes) => {
  const server = api(routes)
  const callbacks = { onsaved: vi.fn(), oncancel: vi.fn() }
  const view = render(SiteWizard, { previewDelay: 0, ...callbacks, ...props })
  return { ...view, ...callbacks, server }
}

const fill = (label, value) => fireEvent.input(screen.getByLabelText(label), { target: { value } })
const button = (name) => screen.getByRole('button', { name })
const fillValidSite = async () => {
  await fill('Hostname', 'app.example.com')
  await fill('Upstreams', '10.0.0.5:8080')
}

const existing = {
  id: ID,
  hostname: 'app.example.com',
  kind: 'proxy',
  upstreams: ['https://10.0.0.5:8443', '10.0.0.6:8443'],
  skipTlsVerify: true,
  tls: 'internal',
  redirectTarget: '',
  redirectCode: 0,
  accessLog: false,
  enabled: false,
}

afterEach(() => vi.unstubAllGlobals())

describe('SiteWizard for a new site', () => {
  it('opens with a reverse proxy, automatic TLS and nothing asked of the server yet', () => {
    const { server } = open()
    expect(screen.getByRole('heading', { name: 'Add a site' })).toBeTruthy()
    expect(screen.getByLabelText('Reverse proxy').checked).toBe(true)
    expect(screen.getByLabelText('Redirect').checked).toBe(false)
    expect(screen.getByLabelText('TLS').value).toBe('automatic')
    expect(screen.getByLabelText('Enabled').checked).toBe(true)
    expect(button('Add site').disabled).toBe(true)
    expect(server.calls).toHaveLength(0)
  })

  it('has every field the API takes', () => {
    open({ accessLog: true })
    for (const label of ['Hostname', 'Upstreams', 'Skip TLS verification', 'TLS', 'Log requests', 'Enabled']) {
      expect(screen.getByLabelText(label), label).toBeTruthy()
    }
  })

  it('asks the server about what is typed and shows the text it would write', async () => {
    const { server } = open()
    await fillValidSite()
    await waitFor(() => expect(screen.getByLabelText('Generated Caddyfile').textContent).toContain('# generated'))
    expect(server.previews().at(-1).body).toEqual({
      hostname: 'app.example.com',
      kind: 'proxy',
      upstreams: ['10.0.0.5:8080'],
      skipTlsVerify: false,
      tls: 'automatic',
      redirectTarget: '',
      redirectCode: 0,
      accessLog: false,
      enabled: true,
      basicAuth: null,
    })
    expect(button('Add site').disabled).toBe(false)
  })

  it('shows the errors of the fields that were touched, and not of the others', async () => {
    open(
      {},
      {
        'POST /api/managed-sites/preview': () =>
          json({
            valid: false,
            errors: [
              { site: 'x', field: 'hostname', message: 'must be a DNS name' },
              { site: 'x', field: 'upstreams', message: 'needs at least one upstream' },
            ],
          }),
      },
    )
    await fill('Hostname', 'not a host')
    expect(await screen.findByText('must be a DNS name')).toBeTruthy()
    expect(screen.getByLabelText('Hostname').getAttribute('aria-invalid')).toBe('true')
    expect(screen.queryByText('needs at least one upstream')).toBeNull()
    expect(screen.getByLabelText('Upstreams').getAttribute('aria-invalid')).not.toBe('true')
    expect(button('Add site').disabled).toBe(true)
  })

  it('shows no generated text for a site that is not valid', async () => {
    open({}, { 'POST /api/managed-sites/preview': () => json({ valid: false, errors: [{ site: 'x', field: 'hostname', message: 'bad' }] }) })
    await fill('Hostname', 'x')
    await screen.findByText('bad')
    expect(screen.queryByLabelText('Generated Caddyfile')).toBeNull()
  })

  it('swaps the proxy fields for the redirect ones', async () => {
    open()
    await fireEvent.click(screen.getByLabelText('Redirect'))
    expect(screen.queryByLabelText('Upstreams')).toBeNull()
    expect(screen.queryByLabelText('Skip TLS verification')).toBeNull()
    expect(screen.getByLabelText('Redirect to')).toBeTruthy()
    const codes = Array.from(screen.getByLabelText('Status code').options).map((o) => o.value)
    expect(codes).toEqual(['301', '302', '307', '308'])
  })

  it('sends a redirect with its own fields only', async () => {
    const { server } = open()
    await fill('Hostname', 'old.example.com')
    await fireEvent.click(screen.getByLabelText('Redirect'))
    await fill('Redirect to', 'https://new.example.com{uri}')
    await fireEvent.change(screen.getByLabelText('Status code'), { target: { value: '308' } })
    await waitFor(() => expect(server.previews().at(-1)?.body.kind).toBe('redirect'))
    await waitFor(() => expect(server.previews().at(-1).body.redirectCode).toBe(308))
    expect(server.previews().at(-1).body).toMatchObject({ upstreams: [], redirectTarget: 'https://new.example.com{uri}', skipTlsVerify: false })
  })

  it('sends the TLS mode that was chosen', async () => {
    const { server } = open()
    await fill('Hostname', 'app.example.com')
    await fireEvent.change(screen.getByLabelText('TLS'), { target: { value: 'off' } })
    await waitFor(() => expect(server.previews().at(-1).body.tls).toBe('off'))
  })

  it('cannot log requests when no access log is known, and says why', () => {
    open({ accessLog: false })
    expect(screen.getByLabelText('Log requests').disabled).toBe(true)
    expect(screen.getByText(/no access log/i)).toBeTruthy()
  })

  it('logs requests when an access log is known and it is asked to', async () => {
    const { server } = open({ accessLog: true })
    expect(screen.getByLabelText('Log requests').disabled).toBe(false)
    await fireEvent.click(screen.getByLabelText('Log requests'))
    await fill('Hostname', 'app.example.com')
    await waitFor(() => expect(server.previews().at(-1)?.body.accessLog).toBe(true))
  })

  it('only the check of the latest text counts', async () => {
    const waiting = []
    open({}, { 'POST /api/managed-sites/preview': (body) => new Promise((resolve) => waiting.push({ body, resolve })) })
    await fill('Hostname', 'first.example.com')
    await waitFor(() => expect(waiting).toHaveLength(1))
    await fill('Hostname', 'second.example.com')
    await waitFor(() => expect(waiting).toHaveLength(2))

    waiting[1].resolve(ok({ valid: true, text: '# for the second\n' }))
    await waitFor(() => expect(screen.getByLabelText('Generated Caddyfile').textContent).toContain('second'))
    waiting[0].resolve(ok({ valid: false, errors: [{ site: 'x', field: 'hostname', message: 'stale' }] }))
    await new Promise((r) => setTimeout(r, 10))
    expect(screen.queryByText('stale')).toBeNull()
    expect(screen.getByLabelText('Generated Caddyfile').textContent).toContain('second')
  })

  it('says when the check itself could not be made', async () => {
    open({}, { 'POST /api/managed-sites/preview': () => json({ error: 'internal error' }, 500) })
    await fill('Hostname', 'app.example.com')
    expect((await screen.findByRole('alert')).textContent).toContain('internal error')
    expect(button('Add site').disabled).toBe(true)
  })
})

describe('SiteWizard saving a new site', () => {
  it('posts the site and tells the page what the server answered', async () => {
    const { server, onsaved } = open()
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))

    await waitFor(() => expect(onsaved).toHaveBeenCalledExactlyOnceWith(savedAnswer))
    expect(server.creates()).toHaveLength(1)
    expect(server.creates()[0].body).toMatchObject({ hostname: 'app.example.com', upstreams: ['10.0.0.5:8080'] })
  })

  it('does not post twice while it waits', async () => {
    let finish
    const { server } = open({}, { 'POST /api/managed-sites': () => new Promise((resolve) => (finish = resolve)) })
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))
    expect(button('Add site').disabled).toBe(true)
    await fireEvent.click(button('Add site'))
    expect(server.creates()).toHaveLength(1)
    finish(ok(savedAnswer))
  })

  it('shows every field the server refused, even those not touched, and stays open', async () => {
    const { onsaved } = open(
      {},
      {
        'POST /api/managed-sites': () =>
          json(
            {
              error: 'Nothing was applied and the sites are unchanged: the site is not valid',
              code: 'invalid-site',
              errors: [
                { site: 'x', field: 'tls', message: 'must be automatic, internal or off' },
                { site: 'x', field: 'upstreams', message: 'has a duplicate' },
              ],
            },
            422,
          ),
      },
    )
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))

    expect(await screen.findByText('must be automatic, internal or off')).toBeTruthy()
    expect(screen.getByText('has a duplicate')).toBeTruthy()
    expect(onsaved).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Hostname').value).toBe('app.example.com')
  })

  it('says what Caddy refused and that nothing was applied', async () => {
    const { onsaved } = open(
      {},
      {
        'POST /api/managed-sites': () =>
          json(
            { error: 'Nothing was applied and the sites are unchanged: caddy rejected the site app.example.com: adapting config using caddyfile adapter: ambiguous site definition', code: 'site-rejected', site: { id: 'x', hostname: 'app.example.com' } },
            422,
          ),
      },
    )
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('Nothing was applied')
    expect(alert.textContent).toContain('ambiguous site definition')
    expect(alert.textContent).not.toContain('adapting config')
    expect(onsaved).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Hostname').value).toBe('app.example.com')
  })

  it('says why an apply failed', async () => {
    open({}, { 'POST /api/managed-sites': () => json({ error: 'Nothing was applied: caddy refused the load', code: 'apply-failed' }, 502) })
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))
    expect((await screen.findByRole('alert')).textContent).toContain('caddy refused the load')
  })

  it('clears the failure once the form is edited', async () => {
    open({}, { 'POST /api/managed-sites': () => json({ error: 'Nothing was applied: boom', code: 'apply-failed' }, 502) })
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))
    await screen.findByRole('alert')
    await fill('Hostname', 'other.example.com')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders hostile text as text', async () => {
    const { container } = open({}, { 'POST /api/managed-sites': () => json({ error: '<img src=x onerror=alert(1)>', code: 'apply-failed' }, 502) })
    await fillValidSite()
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))
    expect((await screen.findByRole('alert')).textContent).toContain('<img')
    expect(container.querySelector('img')).toBeNull()
  })

  it('cancels without asking the server to add anything', async () => {
    const { server, oncancel } = open()
    await fireEvent.click(button('Cancel'))
    expect(oncancel).toHaveBeenCalledOnce()
    expect(server.creates()).toHaveLength(0)
  })
})

describe('SiteWizard changing a site', () => {
  it('opens with the site, and a title that names it', () => {
    open({ site: existing })
    expect(screen.getByRole('heading', { name: 'Edit app.example.com' })).toBeTruthy()
    expect(screen.getByLabelText('Hostname').value).toBe('app.example.com')
    expect(screen.getByLabelText('Upstreams').value).toBe('https://10.0.0.5:8443\n10.0.0.6:8443')
    expect(screen.getByLabelText('Skip TLS verification').checked).toBe(true)
    expect(screen.getByLabelText('TLS').value).toBe('internal')
    expect(screen.getByLabelText('Enabled').checked).toBe(false)
  })

  it('opens a redirect on its fields', () => {
    open({ site: { ...existing, kind: 'redirect', upstreams: [], redirectTarget: 'https://x.example.com', redirectCode: 307, skipTlsVerify: false } })
    expect(screen.getByLabelText('Redirect').checked).toBe(true)
    expect(screen.getByLabelText('Redirect to').value).toBe('https://x.example.com')
    expect(screen.getByLabelText('Status code').value).toBe('307')
  })

  it('checks it straight away, naming the site it replaces', async () => {
    const { server } = open({ site: existing })
    await waitFor(() => expect(server.previews()).toHaveLength(1))
    expect(server.previews()[0].body).toMatchObject({ id: ID, hostname: 'app.example.com' })
    await waitFor(() => expect(button('Save changes').disabled).toBe(false))
  })

  it('puts the changes to the site and tells the page', async () => {
    const { server, onsaved } = open({ site: existing })
    await waitFor(() => expect(button('Save changes').disabled).toBe(false))
    await fill('Hostname', 'renamed.example.com')
    await waitFor(() => expect(server.previews().at(-1).body.hostname).toBe('renamed.example.com'))
    await fireEvent.click(button('Save changes'))

    await waitFor(() => expect(onsaved).toHaveBeenCalledExactlyOnceWith(savedAnswer))
    expect(server.updates()).toHaveLength(1)
    expect(server.updates()[0].body).toMatchObject({ hostname: 'renamed.example.com', enabled: false })
    expect(server.updates()[0].body.id).toBeUndefined()
    expect(server.creates()).toHaveLength(0)
  })
})

describe('SiteWizard password protection', () => {
  const protectedSite = { ...existing, basicAuth: { user: 'bob' } }
  const lastBody = (server) => server.previews().at(-1)?.body

  it('has a switch for it, off for a new site, with no username or password to fill in', () => {
    open()
    expect(screen.getByLabelText('Require a password').checked).toBe(false)
    expect(screen.queryByLabelText('Username')).toBeNull()
    expect(screen.queryByLabelText('Password')).toBeNull()
  })

  it('asks for a username and a password when the switch is on', async () => {
    open()
    await fireEvent.click(screen.getByLabelText('Require a password'))
    expect(screen.getByLabelText('Username')).toBeTruthy()
    const password = screen.getByLabelText('Password')
    expect(password.type).toBe('password')
    expect(password.getAttribute('autocomplete')).toBe('new-password')
  })

  it('sends the username and the password it is given', async () => {
    const { server } = open()
    await fill('Hostname', 'app.example.com')
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await fill('Username', 'bob')
    await fill('Password', 'correct horse')
    await waitFor(() => expect(lastBody(server).basicAuth).toEqual({ user: 'bob', password: 'correct horse' }))
  })

  it('sends none when the switch is off, whatever was typed before', async () => {
    const { server } = open()
    await fill('Hostname', 'app.example.com')
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await fill('Username', 'bob')
    await fill('Password', 'correct horse')
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await waitFor(() => expect(lastBody(server).basicAuth).toBeNull())
    expect(screen.queryByLabelText('Password')).toBeNull()
  })

  it('does not offer it for a redirect, and sends none', async () => {
    const { server } = open()
    await fill('Hostname', 'old.example.com')
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await fill('Username', 'bob')
    await fill('Password', 'correct horse')
    await fireEvent.click(screen.getByLabelText('Redirect'))
    expect(screen.queryByLabelText('Require a password')).toBeNull()
    await waitFor(() => expect(lastBody(server).kind).toBe('redirect'))
    expect(lastBody(server).basicAuth).toBeNull()
  })

  it('warns that the password travels unencrypted when TLS is off', async () => {
    open()
    await fireEvent.click(screen.getByLabelText('Require a password'))
    expect(screen.queryByText(/unencrypted/i)).toBeNull()
    await fireEvent.change(screen.getByLabelText('TLS'), { target: { value: 'off' } })
    expect(screen.getByText(/unencrypted/i)).toBeTruthy()
    await fireEvent.change(screen.getByLabelText('TLS'), { target: { value: 'internal' } })
    expect(screen.queryByText(/unencrypted/i)).toBeNull()
  })

  it('does not warn about it when there is no password', async () => {
    open()
    await fireEvent.change(screen.getByLabelText('TLS'), { target: { value: 'off' } })
    expect(screen.queryByText(/unencrypted/i)).toBeNull()
  })

  it('shows the errors of the username and of the password under their fields', async () => {
    open(
      {},
      {
        'POST /api/managed-sites/preview': () =>
          json({
            valid: false,
            errors: [
              { site: 'x', field: 'basicAuthUser', message: 'Use letters, digits and . _ - @ in the username.' },
              { site: 'x', field: 'basicAuthPassword', message: 'The password needs at least 8 characters.' },
            ],
          }),
      },
    )
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await fill('Username', 'bob smith')
    await fill('Password', 'short')
    expect(await screen.findByText('Use letters, digits and . _ - @ in the username.')).toBeTruthy()
    expect(screen.getByText('The password needs at least 8 characters.')).toBeTruthy()
    expect(screen.getByLabelText('Username').getAttribute('aria-invalid')).toBe('true')
    expect(screen.getByLabelText('Password').getAttribute('aria-invalid')).toBe('true')
    expect(button('Add site').disabled).toBe(true)
  })

  it('keeps what was typed in the password field when the server refuses the site', async () => {
    open({}, { 'POST /api/managed-sites': () => json({ error: 'Nothing was applied: boom', code: 'apply-failed' }, 502) })
    await fill('Hostname', 'app.example.com')
    await fill('Upstreams', '10.0.0.5:8080')
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await fill('Username', 'bob')
    await fill('Password', 'correct horse')
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))
    await screen.findByRole('alert')
    expect(screen.getByLabelText('Password').value).toBe('correct horse')
  })

  it('posts the password with the site', async () => {
    const { server } = open()
    await fill('Hostname', 'app.example.com')
    await fill('Upstreams', '10.0.0.5:8080')
    await fireEvent.click(screen.getByLabelText('Require a password'))
    await fill('Username', 'bob')
    await fill('Password', 'correct horse')
    await waitFor(() => expect(button('Add site').disabled).toBe(false))
    await fireEvent.click(button('Add site'))
    await waitFor(() => expect(server.creates()).toHaveLength(1))
    expect(server.creates()[0].body.basicAuth).toEqual({ user: 'bob', password: 'correct horse' })
  })

  describe('of a site that has a password', () => {
    it('opens with the switch on and the username, and never with the password', () => {
      open({ site: protectedSite })
      expect(screen.getByLabelText('Require a password').checked).toBe(true)
      expect(screen.getByLabelText('Username').value).toBe('bob')
      expect(screen.getByLabelText('Password').value).toBe('')
    })

    it('says a password is set, and that leaving the field empty keeps it', () => {
      open({ site: protectedSite })
      expect(screen.getByText(/a password is set/i)).toBeTruthy()
      expect(screen.getByText(/leave the password empty to keep it/i)).toBeTruthy()
    })

    it('keeps the password when it is saved without a new one', async () => {
      const { server } = open({ site: protectedSite })
      await waitFor(() => expect(button('Save changes').disabled).toBe(false))
      expect(lastBody(server).basicAuth).toEqual({ user: 'bob', password: '' })
      await fireEvent.click(button('Save changes'))
      await waitFor(() => expect(server.updates()).toHaveLength(1))
      expect(server.updates()[0].body.basicAuth).toEqual({ user: 'bob', password: '' })
    })

    it('sends a new password when one is typed', async () => {
      const { server } = open({ site: protectedSite })
      await fill('Password', 'a new password')
      await waitFor(() => expect(lastBody(server).basicAuth).toEqual({ user: 'bob', password: 'a new password' }))
    })

    it('takes the password away when the switch is turned off', async () => {
      const { server } = open({ site: protectedSite })
      await fireEvent.click(screen.getByLabelText('Require a password'))
      await waitFor(() => expect(lastBody(server).basicAuth).toBeNull())
      expect(screen.queryByText(/a password is set/i)).toBeNull()
    })

    it('does not say a password is set for a site that has none', async () => {
      open({ site: existing })
      await fireEvent.click(screen.getByLabelText('Require a password'))
      expect(screen.queryByText(/a password is set/i)).toBeNull()
    })
  })
})
