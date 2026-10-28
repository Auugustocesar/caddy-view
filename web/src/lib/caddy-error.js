const ADAPTER_PREFIX = /^adapting config using caddyfile adapter:\s*/
// Where the error is, and the chain of imports that led there: the page shows the line, and marks it.
const POSITION_SUFFIX = /,?\s+at \S+:\d+(?:\s+import chain: \[.*\])?\s*$/

/** What Caddy said, without the parts that only repeat what the page already shows. @param {string} message @returns {string} */
export function tidyCaddyError(message) {
  const tidy = message.replace(ADAPTER_PREFIX, '').replace(POSITION_SUFFIX, '')
  // Never leave nothing to read.
  return tidy.trim() === '' ? message : tidy
}
