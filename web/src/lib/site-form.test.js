import { describe, expect, it } from 'vitest'
import { REDIRECT_CODES, describeSite, emptyForm, errorsByField, formFromSite, isProtected, payloadFromForm } from './site-form.js'

const proxySite = {
  id: 'a'.repeat(32),
  hostname: 'app.example.com',
  kind: 'proxy',
  upstreams: ['https://10.0.0.5:8443', '10.0.0.6:8443'],
  skipTlsVerify: true,
  tls: 'internal',
  redirectTarget: '',
  redirectCode: 0,
  accessLog: true,
  enabled: false,
  createdAt: '2026-10-28T10:00:00Z',
  updatedAt: '2026-10-28T10:00:00Z',
}

const redirectSite = {
  id: 'b'.repeat(32),
  hostname: 'old.example.com',
  kind: 'redirect',
  upstreams: [],
  skipTlsVerify: false,
  tls: 'automatic',
  redirectTarget: 'https://new.example.com{uri}',
  redirectCode: 308,
  accessLog: false,
  enabled: true,
}

describe('emptyForm', () => {
  it('starts a reverse proxy with Caddy looking after the certificate, enabled', () => {
    expect(emptyForm()).toEqual({
      hostname: '',
      kind: 'proxy',
      upstreams: '',
      skipTlsVerify: false,
      tls: 'automatic',
      redirectTarget: '',
      redirectCode: 301,
      accessLog: false,
      enabled: true,
      basicAuth: false,
      basicAuthUser: '',
      basicAuthPassword: '',
      hasPassword: false,
    })
  })

  it('gives a new object every time', () => {
    expect(emptyForm()).not.toBe(emptyForm())
  })
})

describe('formFromSite', () => {
  it('puts the upstreams one to a line', () => {
    expect(formFromSite(proxySite).upstreams).toBe('https://10.0.0.5:8443\n10.0.0.6:8443')
  })

  it('keeps every field the form has', () => {
    expect(formFromSite(proxySite)).toEqual({
      hostname: 'app.example.com',
      kind: 'proxy',
      upstreams: 'https://10.0.0.5:8443\n10.0.0.6:8443',
      skipTlsVerify: true,
      tls: 'internal',
      redirectTarget: '',
      redirectCode: 301,
      accessLog: true,
      enabled: false,
      basicAuth: false,
      basicAuthUser: '',
      basicAuthPassword: '',
      hasPassword: false,
    })
  })

  it('takes the code of a redirect, and 301 when a proxy has none', () => {
    expect(formFromSite(redirectSite).redirectCode).toBe(308)
    expect(formFromSite(proxySite).redirectCode).toBe(301)
  })

  it('has no upstreams text for a redirect', () => {
    expect(formFromSite(redirectSite).upstreams).toBe('')
  })
})

describe('payloadFromForm', () => {
  const form = { ...emptyForm(), hostname: '  app.example.com ', upstreams: ' 10.0.0.5:8080 \n\n  https://10.0.0.6:8443\r\n', accessLog: true }

  it('trims the hostname and splits the upstreams into trimmed lines, dropping the empty ones', () => {
    const payload = payloadFromForm(form)
    expect(payload.hostname).toBe('app.example.com')
    expect(payload.upstreams).toEqual(['10.0.0.5:8080', 'https://10.0.0.6:8443'])
  })

  it('sends a proxy without the fields of a redirect', () => {
    const payload = payloadFromForm({ ...form, redirectTarget: 'https://x.example.com', redirectCode: 308 })
    expect(payload).toMatchObject({ kind: 'proxy', redirectTarget: '', redirectCode: 0 })
  })

  it('sends a redirect without the fields of a proxy', () => {
    const payload = payloadFromForm({ ...form, kind: 'redirect', redirectTarget: ' https://x.example.com{uri} ', redirectCode: 302, skipTlsVerify: true })
    expect(payload).toMatchObject({ kind: 'redirect', upstreams: [], skipTlsVerify: false, redirectTarget: 'https://x.example.com{uri}', redirectCode: 302 })
  })

  it('sends the other choices as they are', () => {
    expect(payloadFromForm({ ...form, tls: 'off', enabled: false })).toMatchObject({ tls: 'off', enabled: false, accessLog: true })
  })

  it('sends only the fields the API knows, and numbers as numbers', () => {
    const payload = payloadFromForm({ ...form, kind: 'redirect', redirectCode: '307' })
    expect(Object.keys(payload).sort()).toEqual(
      ['accessLog', 'basicAuth', 'enabled', 'hostname', 'kind', 'redirectCode', 'redirectTarget', 'skipTlsVerify', 'tls', 'upstreams'],
    )
    expect(payload.redirectCode).toBe(307)
  })

  it('round-trips a site through the form', () => {
    const payload = payloadFromForm(formFromSite(proxySite))
    expect(payload).toEqual({
      hostname: 'app.example.com',
      kind: 'proxy',
      upstreams: ['https://10.0.0.5:8443', '10.0.0.6:8443'],
      skipTlsVerify: true,
      tls: 'internal',
      redirectTarget: '',
      redirectCode: 0,
      accessLog: true,
      enabled: false,
      basicAuth: null,
    })
    expect(payloadFromForm(formFromSite(redirectSite))).toMatchObject({ kind: 'redirect', redirectTarget: 'https://new.example.com{uri}', redirectCode: 308 })
  })
})

describe('errorsByField', () => {
  it('maps each field to its first message', () => {
    expect(
      errorsByField([
        { site: 'x', field: 'hostname', message: 'is required' },
        { site: 'x', field: 'upstreams', message: 'needs one' },
        { site: 'x', field: 'hostname', message: 'second one' },
      ]),
    ).toEqual({ hostname: 'is required', upstreams: 'needs one' })
  })

  it.each([[undefined], [null], [[]]])('is empty for %j', (errors) => {
    expect(errorsByField(errors)).toEqual({})
  })
})

describe('describeSite', () => {
  it('says where a proxy goes', () => {
    expect(describeSite(proxySite)).toBe('Proxy to https://10.0.0.5:8443, 10.0.0.6:8443')
  })

  it('says where a redirect goes, and with which code', () => {
    expect(describeSite(redirectSite)).toBe('Redirect to https://new.example.com{uri} (308)')
  })

  it('does not break on a site with no upstreams', () => {
    expect(describeSite({ ...proxySite, upstreams: [] })).toBe('Proxy')
  })
})

describe('REDIRECT_CODES', () => {
  it('are the four the server accepts', () => {
    expect(REDIRECT_CODES).toEqual([301, 302, 307, 308])
  })
})

const protectedSite = { ...proxySite, basicAuth: { user: 'bob' } }

describe('the password of a site, in the form', () => {
  it('starts without one', () => {
    const form = emptyForm()
    expect([form.basicAuth, form.basicAuthUser, form.basicAuthPassword, form.hasPassword]).toEqual([false, '', '', false])
  })

  it('opens a protected site with its user, a password that is set, and no password typed', () => {
    const form = formFromSite(protectedSite)
    expect([form.basicAuth, form.basicAuthUser, form.basicAuthPassword, form.hasPassword]).toEqual([true, 'bob', '', true])
  })

  it('opens a site that has none, and one whose basicAuth is null, as unprotected', () => {
    for (const site of [proxySite, { ...proxySite, basicAuth: null }]) {
      const form = formFromSite(site)
      expect([form.basicAuth, form.basicAuthUser, form.hasPassword]).toEqual([false, '', false])
    }
  })

  it('sends the user and the password when the form asks for one', () => {
    const form = { ...emptyForm(), hostname: 'app.example.com', basicAuth: true, basicAuthUser: ' bob ', basicAuthPassword: ' correct horse ' }
    expect(payloadFromForm(form).basicAuth).toEqual({ user: 'bob', password: ' correct horse ' })
  })

  it('sends no password when the form does not ask for one, whatever was typed', () => {
    const form = { ...emptyForm(), hostname: 'app.example.com', basicAuth: false, basicAuthUser: 'bob', basicAuthPassword: 'correct horse' }
    expect(payloadFromForm(form).basicAuth).toBeNull()
  })

  it('sends no password for a redirect, which cannot have one', () => {
    const form = { ...emptyForm(), kind: 'redirect', basicAuth: true, basicAuthUser: 'bob', basicAuthPassword: 'correct horse' }
    expect(payloadFromForm(form).basicAuth).toBeNull()
  })

  it('keeps the password a site has when it is sent back without a new one', () => {
    expect(payloadFromForm(formFromSite(protectedSite)).basicAuth).toEqual({ user: 'bob', password: '' })
  })

  it('never carries a password out of a site, because the API never gives one', () => {
    expect(JSON.stringify(formFromSite({ ...protectedSite, basicAuth: { user: 'bob', hash: 'x', password: 'y' } }))).not.toMatch(/"x"|"y"/)
  })
})

describe('isProtected', () => {
  it('is true for a site with a user', () => {
    expect(isProtected(protectedSite)).toBe(true)
  })

  it.each([[proxySite], [{ ...proxySite, basicAuth: null }], [{ ...proxySite, basicAuth: undefined }]])('is false for %j', (site) => {
    expect(isProtected(site)).toBe(false)
  })
})
