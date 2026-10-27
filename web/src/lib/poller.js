/**
 * Calls `fn` now and then every `intervalMs`, skipping ticks while the page is hidden and while the previous call
 * is still running. Returns a function that stops it.
 * @param {() => Promise<void>} fn
 * @param {number} intervalMs
 * @param {Pick<Document, 'hidden'|'addEventListener'|'removeEventListener'>} [doc]
 * @returns {() => void}
 */
export function startPolling(fn, intervalMs, doc = document) {
  let running = false
  let stopped = false

  async function run() {
    if (stopped || running || doc.hidden) return
    running = true
    try {
      await fn()
    } catch {
      // The caller reports its own errors, and the next tick tries again.
    } finally {
      running = false
    }
  }

  const timer = setInterval(run, intervalMs)
  const onVisibility = () => {
    if (!doc.hidden) run()
  }
  doc.addEventListener('visibilitychange', onVisibility)
  run()

  return () => {
    stopped = true
    clearInterval(timer)
    doc.removeEventListener('visibilitychange', onVisibility)
  }
}
