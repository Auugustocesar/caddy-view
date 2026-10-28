/**
 * Error from the caddy-view API. `status` is 0 when the server could not be reached at all; `code` and `line` are what
 * a refused change says about itself.
 */
export class ApiError extends Error {
  /** @param {string} message @param {number} status @param {{code?: string, line?: number}} [details] */
  constructor(message, status, details = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    if (details.code !== undefined) this.code = details.code
    if (details.line !== undefined) this.line = details.line
  }
}

/** @param {string} path @param {RequestInit} [init] @returns {Promise<any>} */
async function request(path, init) {
  let res
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError('cannot reach caddy-view', 0)
  }

  let body
  try {
    body = await res.json()
  } catch {
    // Not JSON: an error page from a proxy in front of caddy-view, for example.
    throw new ApiError(res.ok ? 'unexpected response from caddy-view' : `HTTP ${res.status}`, res.status)
  }
  if (!res.ok) {
    throw new ApiError(typeof body?.error === 'string' ? body.error : `HTTP ${res.status}`, res.status, {
      code: typeof body?.code === 'string' ? body.code : undefined,
      line: typeof body?.line === 'number' ? body.line : undefined,
    })
  }
  return body
}

/** @param {string} path @returns {Promise<any>} */
function get(path) {
  return request(path, { headers: { Accept: 'application/json' } })
}

/** @param {string} path @param {object} body @returns {Promise<any>} */
function post(path, body) {
  return request(path, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/** @returns {Promise<{caddy: {reachable: boolean, endpoint: string, error?: string}}>} */
export function getStatus() {
  return get('/api/status')
}

/** @returns {Promise<{sites: Array<{server: string, host: string, upstreams: Array<{address: string, numRequests: number, fails: number}>}>, upstreamsAvailable: boolean}>} */
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
 * @returns {Promise<{window: string, requests: number, requestsPerSecond: number, errors5xx: number, errorRate: number|null, avgMs: number|null, p95Ms: number|null, bytes: number, status: Record<string, number>, inFlight: number|null, upstreams: {healthy: number, total: number}|null}>}
 */
export function getSummary(window) {
  return get(`/api/summary?window=${encodeURIComponent(window)}`)
}

/**
 * Traffic per bucket. Answers 404 when the history is off.
 * @param {'minute'|'hour'} resolution
 * @returns {Promise<{resolution: string, bucketSeconds: number, buckets: Array<{start: string, requests: number, bytes: number, status: Record<string, number>, avgMs: number|null, p95Ms: number|null, maxMs: number|null}>}>}
 */
export function getTraffic(resolution) {
  return get(`/api/traffic?resolution=${encodeURIComponent(resolution)}`)
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
