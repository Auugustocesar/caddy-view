/**
 * A tokenizer for Caddyfile text, in the shape CodeMirror's StreamLanguage takes. It colours what a person scanning a
 * Caddyfile looks for: comments, strings, the site addresses, the directives, placeholders, matchers and snippets, and
 * the braces. It reads the text as `internal/caddyfile` does: a { whose } comes before any white space is a
 * placeholder, any other { opens a block.
 *
 * The state is plain values, so CodeMirror can copy it.
 * @typedef {{depth: number, first: boolean, address: boolean, quote: string|null, heredoc: string|null}} State
 */

const HEREDOC_OPENER = /^<<([A-Za-z0-9_-]+)\s*$/

/** The length of the placeholder at the start of `rest`, or 0 when that { opens a block. @param {string} rest */
function placeholderLength(rest) {
  let depth = 0
  for (let i = 0; i < rest.length; i++) {
    const c = rest[i]
    if (/\s/.test(c)) return 0
    if (c === '{') depth++
    else if (c === '}' && --depth === 0) return i + 1
  }
  return 0
}

/** Reads on to the end of a quoted string, or to the end of the line when it goes on. @param {import('@codemirror/language').StringStream} stream @param {State} state @param {string} quote */
function readString(stream, state, quote) {
  while (!stream.eol()) {
    const c = stream.next()
    if (c === '\\' && quote === '"') stream.next()
    else if (c === quote) {
      state.quote = null
      return 'string'
    }
  }
  state.quote = quote
  return 'string'
}

/** @type {import('@codemirror/language').StreamParser<State>} */
export const caddyfileTokenizer = {
  name: 'caddyfile',

  startState: () => ({ depth: 0, first: true, address: false, quote: null, heredoc: null }),

  // A line sits one unit in for each block open at its start; a line that begins with } sits one unit less.
  indent(state, textAfter, context) {
    if (state.quote !== null || state.heredoc !== null) return null
    return Math.max(0, state.depth - (textAfter.trimStart().startsWith('}') ? 1 : 0)) * context.unit
  },

  languageData: {
    closeBrackets: { brackets: ['(', '{', '"', '`'] },
    indentOnInput: /^\s*\}$/,
  },

  token(stream, state) {
    const insideText = state.quote !== null || state.heredoc !== null
    if (stream.sol() && !insideText) {
      state.first = true
      state.address = false
    }

    if (state.heredoc !== null) {
      const rest = stream.string.slice(stream.pos)
      const indent = rest.length - rest.trimStart().length
      const after = rest.slice(indent)
      const marker = state.heredoc
      if (after.startsWith(marker) && (after.length === marker.length || /\s/.test(after[marker.length]))) {
        stream.pos += indent + marker.length
        state.heredoc = null
      } else {
        stream.skipToEnd()
      }
      return 'string'
    }
    if (state.quote !== null) return readString(stream, state, state.quote)

    if (stream.eatSpace()) return null

    const rest = stream.string.slice(stream.pos)
    const ch = stream.peek()
    const startOfWord = state.first
    state.first = false

    if (ch === '#') {
      stream.skipToEnd()
      return 'comment'
    }
    if (ch === '"' || ch === '`') {
      stream.next()
      return readString(stream, state, ch)
    }
    const heredoc = HEREDOC_OPENER.exec(rest)
    if (heredoc) {
      state.heredoc = heredoc[1]
      stream.skipToEnd()
      return 'string'
    }
    if (ch === '}') {
      stream.next()
      state.depth = Math.max(0, state.depth - 1)
      return 'bracket'
    }
    if (ch === '{') {
      const length = placeholderLength(rest)
      if (length > 0) {
        stream.pos += length
        return 'variableName'
      }
      stream.next()
      state.depth++
      return 'bracket'
    }

    // A word runs to white space, or to a brace, which the next call reads.
    const word = readWord(stream)
    if (word.startsWith('@')) return 'labelName'
    if (startOfWord) {
      if (state.depth === 0) {
        if (/^\(.+\)$/.test(word)) return 'typeName'
        if (word === 'import') return 'keyword'
        state.address = true
        return 'heading'
      }
      return 'keyword'
    }
    return state.depth === 0 && state.address ? 'heading' : null
  },
}

/** @param {import('@codemirror/language').StringStream} stream @returns {string} */
function readWord(stream) {
  const start = stream.pos
  stream.next()
  for (let c = stream.peek(); c !== undefined && !/\s/.test(c) && c !== '{' && c !== '}'; c = stream.peek()) stream.next()
  return stream.string.slice(start, stream.pos)
}
