import { describe, expect, it } from 'vitest'
import { tidyCaddyError } from './caddy-error.js'

describe('tidyCaddyError', () => {
  it.each([
    [
      "adapting config using caddyfile adapter: syntax error: unexpected token 'x', expecting '}', at Caddyfile:25 import chain: ['']",
      "syntax error: unexpected token 'x', expecting '}'",
    ],
    [
      "adapting config using caddyfile adapter: unrecognized directive: nope, at Caddyfile:2 import chain: ['']",
      'unrecognized directive: nope',
    ],
    [
      "adapting config using caddyfile adapter: parsing caddyfile tokens for 'reverse_proxy': wrong argument count or unexpected line ending after 'x', at Caddyfile:22 import chain: ['']",
      "parsing caddyfile tokens for 'reverse_proxy': wrong argument count or unexpected line ending after 'x'",
    ],
    ['adapting config using caddyfile adapter: unrecognized directive: nope, at Caddyfile:2', 'unrecognized directive: nope'],
    ['ambiguous site definition: http://hand.localhost', 'ambiguous site definition: http://hand.localhost'],
    ['unrecognized directive: nope', 'unrecognized directive: nope'],
    ['', ''],
  ])('%j becomes %j', (said, want) => {
    expect(tidyCaddyError(said)).toBe(want)
  })

  it('keeps what it does not know how to cut', () => {
    expect(tidyCaddyError('something at Caddyfile later: not a position')).toBe('something at Caddyfile later: not a position')
  })

  it('does not leave the message empty when that is all there is to cut', () => {
    expect(tidyCaddyError('adapting config using caddyfile adapter: ')).not.toBe('')
  })
})
