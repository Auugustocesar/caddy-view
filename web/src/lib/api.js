/** Error from the caddy-view API. `status` is 0 when the server could not be reached at all. */
export class ApiError extends Error {
  /** @param {string} message @param {number} status */
  constructor(message, status) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

/** @param {string} path @returns {Promise<any>} */
async function get(path) {
  let res
  try {
    res = await fetch(path, { headers: { Accept: 'application/json' } })
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
    throw new ApiError(typeof body?.error === 'string' ? body.error : `HTTP ${res.status}`, res.status)
  }
  return body
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
