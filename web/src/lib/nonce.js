const PLACEHOLDER = '__CSP_NONCE__'

/**
 * The nonce the server put in the page for inline styles, or an empty string when there is none. The code editor adds
 * <style> elements, and the Content-Security-Policy admits only those that carry this value.
 * @param {Document} [doc]
 * @returns {string}
 */
export function cspNonce(doc = document) {
  const content = doc.querySelector('meta[name="csp-nonce"]')?.getAttribute('content') ?? ''
  // Served by something other than caddy-view (the dev server), the placeholder is still there.
  return content === PLACEHOLDER ? '' : content
}
