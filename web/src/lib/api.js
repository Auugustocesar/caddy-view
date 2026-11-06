/**
 * Error from the caddy-view API. `status` is 0 when the server could not be reached at all; `code` and `line` are what
 * a refused change says about itself.
 */
export class ApiError extends Error {
  /** @param {string} message @param {number} status @param {{retryAfter?: number, code?: string, line?: number, errors?: Array<object>, site?: object}} [details] */
  constructor(message, status, details = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    // How long a held back login must wait, in seconds.
    if (details.retryAfter !== undefined) this.retryAfter = details.retryAfter
    if (details.code !== undefined) this.code = details.code
    if (details.line !== undefined) this.line = details.line
    // The fields of a managed site that were refused, and the site Caddy rejected.
    if (details.errors !== undefined) this.errors = details.errors
    if (details.site !== undefined) this.site = details.site
  }
}

/** What is called when a route other than the session routes answers 401: the session is gone. */
let unauthorizedHandler = null

/** @param {(() => void)|null} handler */
export function setUnauthorizedHandler(handler) {
  unauthorizedHandler = handler
}

/** Says the session is gone, for what cannot go through `request`, such as the log stream. */
export function reportUnauthorized() {
  unauthorizedHandler?.()
}

// These answer 401 as part of their job, so a 401 from them is not "the session is gone".
const SESSION_PATHS = new Set(['/api/login', '/api/logout', '/api/me'])

/** @param {string|null} value the Retry-After header @returns {number|undefined} */
function secondsOf(value) {
  return value !== null && /^\d+$/.test(value) ? Number(value) : undefined
}

/** @param {string} path @param {RequestInit} [init] @returns {Promise<any>} */
async function request(path, init) {
  let res
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError('cannot reach caddy-view', 0)
  }

  if (res.status === 204) return undefined
  if (res.status === 401 && !SESSION_PATHS.has(path)) unauthorizedHandler?.()

  let body
  try {
    body = await res.json()
  } catch {
    // Not JSON: an error page from a proxy in front of caddy-view, for example.
    throw new ApiError(res.ok ? 'unexpected response from caddy-view' : `HTTP ${res.status}`, res.status)
  }
  if (!res.ok) {
    throw new ApiError(typeof body?.error === 'string' ? body.error : `HTTP ${res.status}`, res.status, {
      retryAfter: secondsOf(res.headers.get('Retry-After')),
      code: typeof body?.code === 'string' ? body.code : undefined,
      line: typeof body?.line === 'number' ? body.line : undefined,
      errors: Array.isArray(body?.errors) ? body.errors : undefined,
      site: body?.site && typeof body.site === 'object' ? body.site : undefined,
    })
  }
  return body
}

/** A query string for the parameters that have a value, with a leading ?; empty when none has. @param {Record<string, unknown>} params @returns {string} */
function query(params) {
  const out = new URLSearchParams()
  for (const [name, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') out.set(name, String(value))
  }
  const text = out.toString()
  return text ? `?${text}` : ''
}

/** @param {string} path @returns {Promise<any>} */
function get(path) {
  return request(path, { headers: { Accept: 'application/json' } })
}

/**
 * A change: the server wants JSON on every one, a delete included, whether or not there is a body.
 * @param {'POST'|'PUT'|'DELETE'} method @param {string} path @param {object} [body] @returns {Promise<any>}
 */
function send(method, path, body) {
  return request(path, {
    method,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
}

/** @param {string} path @param {object} body @returns {Promise<any>} */
function post(path, body) {
  return send('POST', path, body)
}

/**
 * Who is logged in. `required` is false when this caddy-view has no login (its routes do not exist); when it is true and
 * `user` is null, a login is needed.
 * @returns {Promise<{required: boolean, user: string|null}>}
 */
export async function getSession() {
  try {
    const body = await get('/api/me')
    return { required: true, user: body.user.username }
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return { required: true, user: null }
    if (err instanceof ApiError && err.status === 404) return { required: false, user: null }
    throw err
  }
}

/** Starts a session; the server sets its cookie. Throws an ApiError, with `retryAfter` when held back. @param {string} username @param {string} password @returns {Promise<string>} the username as stored */
export async function login(username, password) {
  const body = await post('/api/login', { username, password })
  return body.user.username
}

/** Ends the session and clears its cookie. @returns {Promise<void>} */
export async function logout() {
  await post('/api/logout', {})
}

/** @returns {Promise<{caddy: {reachable: boolean, endpoint: string, error?: string}}>} */
export function getStatus() {
  return get('/api/status')
}

/** @returns {Promise<{sites: Array<{server: string, host: string, upstreams: Array<{address: string, numRequests: number, fails: number, state?: 'answering'|'down'|'unknown', latencyMs?: number, httpStatus?: number, checkedAt?: string}>}>, upstreamsAvailable: boolean, upstreamCheck?: boolean, healthChecks?: {proxies: number, withChecks: number}}>} */
export function getSites() {
  return get('/api/sites')
}

/** @returns {Promise<object>} */
export function getConfig() {
  return get('/api/config')
}

/**
 * Totals for a window (5m, 1h, 6h, 24h or 7d). Answers 404 when the history is off.
 * @param {string} window
 * @returns {Promise<{window: string, requests: number, requestsPerSecond: number, errors5xx: number, errorRate: number|null, avgMs: number|null, p95Ms: number|null, bytes: number, status: Record<string, number>, inFlight: number|null, upstreams: {healthy: number, total: number}|null, previous: {requests: number, errors5xx: number, errorRate: number|null, avgMs: number|null, p95Ms: number|null}}>}
 */
export function getSummary(window) {
  return get(`/api/summary?window=${encodeURIComponent(window)}`)
}

/**
 * What needs a look over a window (1h, 6h, 24h or 7d): hosts with more errors than before, hosts far slower than the
 * others, and upstreams that do not answer. Answers 404 when the history is off.
 * @param {string} window
 * @returns {Promise<{window: string, items: Array<object>}>}
 */
export function getAttention(window) {
  return get(`/api/attention?window=${encodeURIComponent(window)}`)
}

/**
 * Traffic per bucket. Answers 404 when the history is off.
 * @param {'minute'|'hour'} resolution
 * @param {{from?: string, to?: string, host?: string}} [options]
 * @returns {Promise<{resolution: string, bucketSeconds: number, buckets: Array<{start: string, requests: number, bytes: number, status: Record<string, number>, avgMs: number|null, p95Ms: number|null, maxMs: number|null}>}>}
 */
export function getTraffic(resolution, options = {}) {
  return get(`/api/traffic${query({ resolution, from: options.from, to: options.to, host: options.host })}`)
}

/**
 * The traffic of each host over a range, the busiest first. Answers 404 when the history is off.
 * @param {'minute'|'hour'} resolution @param {{from?: string, to?: string, limit?: number}} [options]
 * @returns {Promise<{hosts: Array<{host: string, requests: number, bytes: number, errors5xx: number, status: Record<string, number>, avgMs: number|null, p95Ms: number|null, maxMs: number|null}>}>}
 */
export function getHosts(resolution, options = {}) {
  return get(`/api/hosts${query({ resolution, from: options.from, to: options.to, limit: options.limit })}`)
}

/**
 * Stored requests, newest first. Answers 404 when the history is off.
 * @param {{from?: string, to?: string, host?: string, minStatus?: number, maxStatus?: number, q?: string, limit?: number, before?: number}} [filters]
 * @returns {Promise<{entries: Array<{id: number, time: string, host: string, method: string, path: string, status: number, durationMs: number, bytes: number, clientIp: string}>, next: number|null}>}
 */
export function getLogs(filters = {}) {
  return get(`/api/logs${query(filters)}`)
}

/** @returns {Promise<{content: string, sha256: string}>} */
export function getCaddyfile() {
  return get('/api/caddyfile')
}

/** @returns {Promise<{editing: {enabled: boolean, caddyfile?: string}}>} */
export function getSystem() {
  return get('/api/system')
}

/** @param {{limit?: number, before?: number}} [page] @returns {Promise<{revisions: Array<object>, next: number|null}>} */
export function getRevisions(page = {}) {
  const query = new URLSearchParams()
  if (page.limit !== undefined) query.set('limit', String(page.limit))
  if (page.before !== undefined) query.set('before', String(page.before))
  const text = query.toString()
  return get(text ? `/api/revisions?${text}` : '/api/revisions')
}

/** @param {number} number @returns {Promise<object>} */
export function getRevision(number) {
  return get(`/api/revisions/${number}`)
}

/** @param {string} content @returns {Promise<{valid: boolean, error?: string, line?: number, warnings?: Array<object>}>} */
export function validateCaddyfile(content) {
  return post('/api/caddyfile/validate', { content })
}

/** @param {string} content @returns {Promise<{content: string, changed: boolean}>} */
export function formatCaddyfile(content) {
  return post('/api/caddyfile/format', { content })
}

/** @param {string} content @param {string} baseSha256 @returns {Promise<{revision: object}>} */
export function applyCaddyfile(content, baseSha256) {
  return post('/api/caddyfile/apply', { content, baseSha256 })
}

/** @param {number} number @returns {Promise<{revision: object}>} */
export function rollbackRevision(number) {
  return post(`/api/revisions/${number}/rollback`, {})
}

/** @returns {Promise<{sites: Array<object>, managedFile: string, imported: boolean, accessLog: boolean}>} */
export function getManagedSites() {
  return get('/api/managed-sites')
}

/** The generated text for a site, or what is wrong with it. @param {object} site @param {string} [id] the site it would replace @returns {Promise<{valid: boolean, text?: string, errors?: Array<{field: string, message: string}>}>} */
export function previewSite(site, id) {
  return post('/api/managed-sites/preview', id === undefined ? site : { id, ...site })
}

/** @param {object} site @returns {Promise<{site: object, revision: object}>} */
export function createSite(site) {
  return post('/api/managed-sites', site)
}

/** @param {string} id @param {object} site @returns {Promise<{site: object, revision: object}>} */
export function updateSite(id, site) {
  return send('PUT', `/api/managed-sites/${id}`, site)
}

/** @param {string} id @returns {Promise<{revision: object}>} */
export function deleteSite(id) {
  return send('DELETE', `/api/managed-sites/${id}`)
}

/** @returns {Promise<{revision: object}>} */
export function addImport() {
  return post('/api/managed-sites/import', {})
}
