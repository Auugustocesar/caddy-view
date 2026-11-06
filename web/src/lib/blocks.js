import { StringStream } from '@codemirror/language'
import { caddyfileTokenizer } from './caddyfile-language.js'

/**
 * @typedef {{startLine: number, endLine: number, endColumn: number}} BlockRange A block that spans lines: the line
 * that opens it, the line that closes it (both 1-based) and the column of the closing brace on that line.
 */

/**
 * The blocks of a Caddyfile that span more than one line, found with the tokenizer that colours the text, so a brace in
 * a comment, a string, a heredoc or a placeholder is not taken for one. A block that is never closed, and a closing
 * brace with nothing to close, are left out.
 * @param {string} text
 * @returns {BlockRange[]} in the order the blocks close
 */
export function blockRanges(text) {
  const tokenizer = caddyfileTokenizer
  const state = tokenizer.startState(2)
  /** @type {number[]} */
  const open = []
  /** @type {BlockRange[]} */
  const ranges = []

  text.split('\n').forEach((line, index) => {
    const stream = new StringStream(line, 4, 2)
    while (!stream.eol()) {
      stream.start = stream.pos
      const type = tokenizer.token(stream, state)
      if (stream.pos <= stream.start) stream.next() // a tokenizer must advance; this keeps a bug from looping forever
      if (type !== 'bracket') continue
      const brace = line[stream.start]
      if (brace === '{') open.push(index + 1)
      else if (brace === '}') {
        const startLine = open.pop()
        if (startLine !== undefined && startLine < index + 1) ranges.push({ startLine, endLine: index + 1, endColumn: stream.start })
      }
    }
  })
  return ranges
}
