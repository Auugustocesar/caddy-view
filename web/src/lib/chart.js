/** The smallest 1, 2 or 5 times a power of ten that is not below `value`: the top of a chart axis. @param {number} value @returns {number} */
export function niceMax(value) {
  if (!(value > 0)) return 1
  const power = 10 ** Math.floor(Math.log10(value))
  const fraction = value / power
  const step = fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 5 ? 5 : 10
  return step * power
}
