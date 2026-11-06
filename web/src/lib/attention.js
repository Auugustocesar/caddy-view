import { formatMs, formatPercent } from './format.js'

/**
 * How a figure moved against the previous period, in words; null when there is nothing to compare with.
 * `times` says it as a multiple, for a rate; `worseWhenHigher` marks a rise of two times or more as bad, the rule the
 * server uses to flag errors.
 * @param {number|null|undefined} current @param {number|null|undefined} previous
 * @param {{times?: boolean, worseWhenHigher?: boolean}} [options]
 * @returns {{text: string, tone: 'bad'|undefined}|null}
 */
export function compare(current, previous, { times = false, worseWhenHigher = false } = {}) {
  if (current == null || previous == null || previous === 0) return null
  const ratio = current / previous
  if (times) {
    const rounded = ratio.toFixed(1)
    if (rounded === '1.0') return { text: 'the same as the previous period', tone: undefined }
    return { text: `${rounded}x the previous period`, tone: worseWhenHigher && ratio >= 2 ? 'bad' : undefined }
  }
  const percent = Math.round((ratio - 1) * 100)
  if (percent === 0) return { text: 'the same as the previous period', tone: undefined }
  const sign = percent > 0 ? '+' : '−'
  return { text: `${sign}${Math.abs(percent)}% on the previous period`, tone: undefined }
}

const GROUP_FROM = 4 // this many down sites or more are said once

const hostName = (host) => host || '(any host)'

/**
 * The items of /api/attention as the Dashboard says them. Many down sites become one item, so that twenty failures do not
 * make twenty paragraphs.
 * @param {Array<object>|undefined} items
 * @returns {Array<{key: string, severity: 'bad'|'warn'|'info', title: string, text: string, host?: string}>}
 */
export function describeItems(items) {
  const list = items ?? []
  const downs = list.filter((i) => i.kind === 'upstream_down')
  const grouped = downs.length >= GROUP_FROM
  const out = []
  let groupDone = false
  list.forEach((item, index) => {
    if (item.kind === 'upstream_down' && grouped) {
      if (groupDone) return
      groupDone = true
      const allBad = downs.every((d) => d.severity === 'bad')
      out.push({
        key: 'down-group',
        severity: allBad ? 'bad' : 'warn',
        title: allBad ? `${downs.length} sites are down` : `${downs.length} sites have upstreams down`,
        text: downs.map((d) => hostName(d.host)).join(', '),
      })
      return
    }
    out.push(describe(item, index))
  })
  return out
}

function describe(item, index) {
  const host = hostName(item.host)
  const key = `${item.kind}-${item.host ?? ''}-${index}`
  switch (item.kind) {
    case 'upstream_down': {
      const addrs = item.down.join(', ')
      if (item.down.length === item.total) {
        const text =
          item.total === 1
            ? `Its only upstream, ${addrs}, does not answer. Visitors get an error.`
            : `None of its ${item.total} upstreams answer (${addrs}). Visitors get an error.`
        return { key, severity: item.severity, host: item.host, title: `${host} is down`, text }
      }
      return {
        key,
        severity: item.severity,
        host: item.host,
        title: `${host} is degraded`,
        text: `${item.down.length} of ${item.total} upstreams do not answer: ${addrs}. The others still serve visitors.`,
      }
    }
    case 'errors_up': {
      const before = item.previousRate == null ? 'none in the previous period' : `up from ${formatPercent(item.previousRate)}`
      return {
        key,
        severity: item.severity,
        host: item.host,
        title: `${host} returns more errors`,
        text: `${formatPercent(item.rate)} of its requests failed, ${before}.`,
      }
    }
    case 'host_slow':
      return {
        key,
        severity: item.severity,
        host: item.host,
        title: `${host} is slow`,
        text: `Its p95 is ${formatMs(item.p95Ms)}, about ${Math.round(item.p95Ms / item.medianP95Ms)} times the median of your hosts.`,
      }
    default:
      return {
        key,
        severity: item.severity,
        title: 'Caddy has no health checks',
        text: 'It reports every upstream as healthy, a down one included, until a visitor hits the error. caddy-view found out by asking each one itself.',
      }
  }
}
