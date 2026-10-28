import { describe, expect, it } from 'vitest'
import { cspNonce } from './nonce.js'

const pageWith = (content) => {
  const doc = document.implementation.createHTMLDocument('')
  if (content !== undefined) {
    const meta = doc.createElement('meta')
    meta.name = 'csp-nonce'
    meta.content = content
    doc.head.append(meta)
  }
  return doc
}

describe('cspNonce', () => {
  it('reads the nonce the server wrote in the page', () => {
    expect(cspNonce(pageWith('abc123_-XYZ'))).toBe('abc123_-XYZ')
  })

  it('is empty when the page has no such tag', () => {
    expect(cspNonce(pageWith())).toBe('')
  })

  it('is empty when the server did not fill the placeholder in, as under the dev server', () => {
    expect(cspNonce(pageWith('__CSP_NONCE__'))).toBe('')
  })

  it('is empty when the tag is empty', () => {
    expect(cspNonce(pageWith(''))).toBe('')
  })
})
