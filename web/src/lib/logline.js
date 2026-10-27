/**
 * @typedef {object} LogLine
 * @property {string} raw the line as written to the log
 * @property {Date|null} time
 * @property {number|null} status
 * @property {string|null} method
 * @property {string|null} host
 * @property {string|null} uri
 * @property {number|null} duration seconds
 */

const isObject = (v) => typeof v === 'object' && v !== null && !Array.isArray(v)
const text = (v) => (typeof v === 'string' ? v : null)
const number = (v) => (typeof v === 'number' && Number.isFinite(v) ? v : null)

/**
 * Reads the fields the UI shows out of a Caddy JSON access log line. Anything else keeps only `raw`.
 * @param {string} raw
 * @returns {LogLine}
 */
export function parseLogLine(raw) {
  /** @type {LogLine} */
  const line = { raw, time: null, status: null, method: null, host: null, uri: null, duration: null }
  let entry
  try {
    entry = JSON.parse(raw)
  } catch {
    return line
  }
  if (!isObject(entry)) return line

  const ts = number(entry.ts)
  line.time = ts === null ? null : new Date(ts * 1000)
  line.status = number(entry.status)
  line.duration = number(entry.duration)
  if (isObject(entry.request)) {
    line.method = text(entry.request.method)
    line.host = text(entry.request.host)
    line.uri = text(entry.request.uri)
  }
  return line
}

/** @param {number|null} status @returns {'2xx'|'3xx'|'4xx'|'5xx'|'other'} */
export function statusClass(status) {
  if (status === null) return 'other'
  if (status >= 200 && status < 300) return '2xx'
  if (status >= 300 && status < 400) return '3xx'
  if (status >= 400 && status < 500) return '4xx'
  if (status >= 500 && status < 600) return '5xx'
  return 'other'
}

/** @param {number|null} seconds @returns {string} */
export function formatDuration(seconds) {
  if (seconds === null) return ''
  if (seconds < 0.001) return '<1 ms'
  const ms = Math.round(seconds * 1000)
  return ms < 1000 ? `${ms} ms` : `${seconds.toFixed(2)} s`
}

/**
 * Keeps the lines whose raw text contains `filter`, ignoring case. The text is matched literally.
 * @template {{raw: string}} T
 * @param {T[]} lines
 * @param {string} filter
 * @returns {T[]}
 */
export function filterLines(lines, filter) {
  if (filter === '') return lines
  const needle = filter.toLowerCase()
  return lines.filter((l) => l.raw.toLowerCase().includes(needle))
}
