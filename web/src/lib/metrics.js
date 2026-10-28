/**
 * What the Metrics tab computes from the answers of the API: the periods it offers, the tiles it totals up, and the shares
 * of each status class.
 */

/** The periods of the Metrics tab. Minutes for the shortest, hours for the rest, which keeps every chart under 3,000 buckets. */
export const PERIODS = [
  { id: '1h', label: '1 hour', resolution: 'minute', hours: 1 },
  { id: '24h', label: '24 hours', resolution: 'hour', hours: 24 },
  { id: '7d', label: '7 days', resolution: 'hour', hours: 168 },
  { id: '30d', label: '30 days', resolution: 'hour', hours: 720 },
]

/** The status classes in the order they are shown, and what each is called. */
const CLASSES = [
  ['1xx', 'Informational'],
  ['2xx', 'Success'],
  ['3xx', 'Redirect'],
  ['4xx', 'Client error'],
  ['5xx', 'Server error'],
  ['other', 'Other'],
]

/** @param {string} id @returns {typeof PERIODS[number]} the period, or the 24 hours one for an id it does not know */
export function periodById(id) {
  return PERIODS.find((p) => p.id === id) ?? PERIODS[1]
}

/** The resolution, and the RFC 3339 range that ends at `now`, for a period. @param {typeof PERIODS[number]} period @param {Date} now @returns {{resolution: string, from: string, to: string}} */
export function periodRange(period, now) {
  const to = Math.floor(now.getTime() / 1000) * 1000
  const iso = (ms) => new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z')
  return { resolution: period.resolution, from: iso(to - period.hours * 3600 * 1000), to: iso(to) }
}

/** What a chart of requests is called at a resolution. @param {string} resolution @returns {string} */
export function chartTitle(resolution) {
  return `Requests per ${resolution}`
}

/**
 * Totals over traffic buckets, as the API answers them. The average is weighted by the requests of each bucket, which is
 * exact: a bucket's average times its requests is the sum of its durations.
 * @param {Array<{requests: number, bytes: number, status?: Record<string, number>, avgMs: number|null, maxMs: number|null}>|null|undefined} buckets
 * @returns {{requests: number, bytes: number, errors5xx: number, errorRate: number|null, avgMs: number|null, maxMs: number|null, status: Record<string, number>}}
 */
export function summarize(buckets) {
  const status = Object.fromEntries(CLASSES.map(([key]) => [key, 0]))
  let requests = 0
  let bytes = 0
  let durationMs = 0
  let maxMs = null
  for (const b of buckets ?? []) {
    requests += b.requests
    bytes += b.bytes
    for (const key of Object.keys(status)) status[key] += b.status?.[key] ?? 0
    if (b.requests > 0 && b.avgMs !== null && b.avgMs !== undefined) durationMs += b.avgMs * b.requests
    if (b.maxMs !== null && b.maxMs !== undefined) maxMs = maxMs === null ? b.maxMs : Math.max(maxMs, b.maxMs)
  }
  return {
    requests,
    bytes,
    errors5xx: status['5xx'],
    errorRate: requests > 0 ? status['5xx'] / requests : null,
    avgMs: requests > 0 ? durationMs / requests : null,
    maxMs: requests > 0 ? maxMs : null,
    status,
  }
}

/** The name of a status class. @param {string} key @returns {string} */
export function statusLabel(key) {
  return CLASSES.find(([k]) => k === key)?.[1] ?? key
}

/** The status classes that have requests, in order, each with its share of the whole. @param {Record<string, number>|null|undefined} status @returns {Array<{key: string, label: string, count: number, share: number}>} */
export function statusShares(status) {
  const counts = CLASSES.map(([key, label]) => ({ key, label, count: status?.[key] ?? 0 }))
  const total = counts.reduce((sum, c) => sum + c.count, 0)
  return counts.filter((c) => c.count > 0).map((c) => ({ ...c, share: c.count / total }))
}
