const NONE = '–'
const counts = new Intl.NumberFormat('en-US')
const UNITS = ['KB', 'MB', 'GB', 'TB']

const missing = (n) => n === null || n === undefined

/** A whole number with thousands separators; an en dash when there is none. @param {number|null|undefined} n @returns {string} */
export function formatCount(n) {
  return missing(n) ? NONE : counts.format(n)
}

/** A byte count in binary units, one decimal above a kilobyte. @param {number|null|undefined} n @returns {string} */
export function formatBytes(n) {
  if (missing(n)) return NONE
  if (n < 1024) return `${n} B`
  let value = n / 1024
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(1)} ${UNITS[unit]}`
}

/** A rate between 0 and 1 as a percentage; "<0.1 %" for a rate that exists but rounds to nothing. @param {number|null|undefined} rate @returns {string} */
export function formatPercent(rate) {
  if (missing(rate)) return NONE
  const tenths = Math.round(rate * 1000)
  if (rate > 0 && tenths === 0) return '<0.1 %'
  return `${tenths / 10} %`
}

/** A duration in milliseconds, in seconds from one second up. @param {number|null|undefined} ms @returns {string} */
export function formatMs(ms) {
  if (missing(ms)) return NONE
  if (ms === 0) return '0 ms'
  if (ms < 1) return '<1 ms'
  const whole = Math.round(ms)
  if (whole < 1000) return `${whole} ms`
  return `${Math.round(whole / 100) / 10} s`
}

/** The local time of day, as HH:MM. @param {string|Date} when @returns {string} */
export function formatClock(when) {
  const date = new Date(when)
  const two = (n) => String(n).padStart(2, '0')
  return `${two(date.getHours())}:${two(date.getMinutes())}`
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** The local date as day and short month, such as "28 Oct". @param {string|Date} when @returns {string} */
export function formatDay(when) {
  const date = new Date(when)
  return `${date.getDate()} ${MONTHS[date.getMonth()]}`
}

/** The local date and time, such as "28 Oct 14:05". @param {string|Date} when @returns {string} */
export function formatStamp(when) {
  return `${formatDay(when)} ${formatClock(when)}`
}

/** A length of time in the largest whole unit that holds it, such as "14 days"; "for ever" for zero. @param {number} seconds @returns {string} */
export function formatDuration(seconds) {
  if (!seconds) return 'for ever'
  const units = [
    [86400, 'day'],
    [3600, 'hour'],
    [60, 'minute'],
  ]
  for (const [size, name] of units) {
    if (seconds % size === 0) return plural(seconds / size, name)
  }
  return plural(seconds, 'second')
}

const plural = (n, name) => `${n} ${name}${n === 1 ? '' : 's'}`
