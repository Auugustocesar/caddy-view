/**
 * The form of a managed site, and the translation between it and what the API takes and gives. There is no validation
 * here: the one implementation of that is `sites.Validate` on the server, which the wizard asks through a preview.
 *
 * @typedef {{
 *   hostname: string, kind: 'proxy'|'redirect', upstreams: string, skipTlsVerify: boolean, tls: 'automatic'|'internal'|'off',
 *   redirectTarget: string, redirectCode: number, accessLog: boolean, enabled: boolean,
 * }} SiteForm
 *
 * @typedef {{
 *   id?: string, hostname: string, kind: string, upstreams: string[], skipTlsVerify: boolean, tls: string,
 *   redirectTarget: string, redirectCode: number, accessLog: boolean, enabled: boolean,
 * }} Site
 */

/** The codes a redirect may use. */
export const REDIRECT_CODES = [301, 302, 307, 308]

/** @returns {SiteForm} a form for a new site */
export function emptyForm() {
  return {
    hostname: '',
    kind: 'proxy',
    upstreams: '',
    skipTlsVerify: false,
    tls: 'automatic',
    redirectTarget: '',
    redirectCode: 301,
    accessLog: false,
    enabled: true,
  }
}

/** @param {Site} site @returns {SiteForm} */
export function formFromSite(site) {
  return {
    hostname: site.hostname,
    kind: site.kind === 'redirect' ? 'redirect' : 'proxy',
    upstreams: (site.upstreams ?? []).join('\n'),
    skipTlsVerify: Boolean(site.skipTlsVerify),
    tls: /** @type {SiteForm['tls']} */ (site.tls),
    redirectTarget: site.redirectTarget ?? '',
    redirectCode: site.redirectCode || 301,
    accessLog: Boolean(site.accessLog),
    enabled: Boolean(site.enabled),
  }
}

/** What the API takes for a form: only the fields of its kind, the others empty. @param {SiteForm} form @returns {Omit<Site, 'id'>} */
export function payloadFromForm(form) {
  const proxy = form.kind === 'proxy'
  return {
    hostname: form.hostname.trim(),
    kind: form.kind,
    upstreams: proxy
      ? form.upstreams
          .split(/\r?\n/)
          .map((line) => line.trim())
          .filter(Boolean)
      : [],
    skipTlsVerify: proxy && form.skipTlsVerify,
    tls: form.tls,
    redirectTarget: proxy ? '' : form.redirectTarget.trim(),
    redirectCode: proxy ? 0 : Number(form.redirectCode),
    accessLog: form.accessLog,
    enabled: form.enabled,
  }
}

/** The first message of each field, from the errors of a refused site. @param {Array<{field: string, message: string}>|undefined|null} errors @returns {Record<string, string>} */
export function errorsByField(errors) {
  /** @type {Record<string, string>} */
  const out = {}
  for (const { field, message } of errors ?? []) out[field] ??= message
  return out
}

/** What a site does, in a few words, for a table. @param {Site} site @returns {string} */
export function describeSite(site) {
  if (site.kind === 'redirect') return `Redirect to ${site.redirectTarget} (${site.redirectCode})`
  return site.upstreams?.length ? `Proxy to ${site.upstreams.join(', ')}` : 'Proxy'
}
