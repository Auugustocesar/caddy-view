const ORDER = { down: 0, unknown: 1, answering: 2 }

/**
 * One row per upstream of every site, as the Dashboard lists them. An upstream without a state is unknown.
 * @param {{sites: Array<{host: string, upstreams: Array<object>}>}|null|undefined} sites
 * @returns {Array<{host: string, address: string, state: 'answering'|'down'|'unknown', latencyMs?: number, httpStatus?: number, checkedAt?: string, siblings: number}>}
 */
export function flattenUpstreams(sites) {
  const rows = []
  for (const site of sites?.sites ?? []) {
    for (const u of site.upstreams) {
      rows.push({
        host: site.host,
        address: u.address,
        state: u.state ?? 'unknown',
        latencyMs: u.latencyMs,
        httpStatus: u.httpStatus,
        checkedAt: u.checkedAt,
        siblings: site.upstreams.length,
      })
    }
  }
  return rows
}

/** Down first, then unknown, then answering; the order inside each is kept. Does not change its argument. @param {Array<{state: string}>} rows */
export function sortFailingFirst(rows) {
  return rows
    .map((row, index) => ({ row, index }))
    .sort((a, b) => ORDER[a.row.state] - ORDER[b.row.state] || a.index - b.index)
    .map(({ row }) => row)
}

/** @param {Array<{state: string}>} rows @returns {{answering: number, down: number, unknown: number, total: number}} */
export function summarizeUpstreams(rows) {
  const out = { answering: 0, down: 0, unknown: 0, total: rows.length }
  for (const r of rows) out[r.state] += 1
  return out
}

/**
 * The rows to show: every down upstream, then the rest up to `limit` rows, unless `expanded`. `hidden` is how many are
 * left out. The rows must already be sorted failing first.
 * @template {{state: string}} T
 * @param {T[]} rows @param {number} limit @param {boolean} expanded @returns {{rows: T[], hidden: number}}
 */
export function visibleUpstreams(rows, limit, expanded) {
  if (expanded) return { rows, hidden: 0 }
  const down = rows.filter((r) => r.state === 'down').length
  const shown = Math.max(limit, down)
  return { rows: rows.slice(0, shown), hidden: Math.max(0, rows.length - shown) }
}
