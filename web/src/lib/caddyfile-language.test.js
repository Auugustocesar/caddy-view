import { StringStream } from '@codemirror/language'
import { describe, expect, it } from 'vitest'
import { caddyfileTokenizer as lang } from './caddyfile-language.js'

// tokens runs the tokenizer over the text, line by line, and returns [text, style] for every token that has a style.
function tokens(text) {
  const state = lang.startState(2)
  const out = []
  for (const line of text.split('\n')) {
    const stream = new StringStream(line, 4, 2)
    while (!stream.eol()) {
      stream.start = stream.pos
      const style = lang.token(stream, state)
      if (stream.pos === stream.start) throw new Error(`the tokenizer did not move on line ${JSON.stringify(line)}`)
      const piece = line.slice(stream.start, stream.pos)
      if (piece.trim() !== '') out.push([piece, style])
    }
    lang.blankLine?.(state)
  }
  return out
}

const styleOf = (text, piece) => tokens(text).find(([t]) => t === piece)?.[1]

describe('the Caddyfile tokenizer', () => {
  it('always moves forward, whatever the text', () => {
    for (const text of ['', '{', '}', '"unterminated', '`unterminated', '<<EOF\nnever closed', '{{{', '@', '#', '\\', '(x', '\t \t']) {
      expect(() => tokens(text)).not.toThrow()
    }
  })

  it('colours a comment to the end of the line', () => {
    expect(tokens('a # note {')).toEqual([
      ['a', expect.anything()],
      ['# note {', 'comment'],
    ])
  })

  it('does not take a hash inside a word for a comment', () => {
    expect(styleOf('a {\n\tfoo#bar\n}', 'foo#bar')).not.toBe('comment')
  })

  it('colours double-quoted and backtick strings, with their escapes and spaces', () => {
    expect(styleOf('a {\n\trespond "say \\"hi\\" #now" 200\n}', '"say \\"hi\\" #now"')).toBe('string')
    expect(styleOf('a {\n\trespond `x   {y}` 200\n}', '`x   {y}`')).toBe('string')
  })

  it('colours the site address, the directives and the arguments differently', () => {
    const t = Object.fromEntries(tokens('example.com {\n\treverse_proxy 127.0.0.1:8080\n}'))
    expect(t['example.com']).toBe('heading')
    expect(t['reverse_proxy']).toBe('keyword')
    expect(t['127.0.0.1:8080']).toBeNull()
  })

  it('treats the first word of a line inside a block as a directive, at any depth', () => {
    expect(styleOf('a {\n\tb {\n\t\tcdir arg\n\t}\n}', 'cdir')).toBe('keyword')
  })

  it('colours placeholders, matchers and snippet names', () => {
    expect(styleOf('a {\n\theader X {http.request.host}\n}', '{http.request.host}')).toBe('variableName')
    expect(styleOf('a {\n\t@api path /api/*\n}', '@api')).toBe('labelName')
    expect(styleOf('(log) {\n\tlog\n}', '(log)')).toBe('typeName')
  })

  it('colours braces as punctuation and counts them for the directive rule', () => {
    expect(tokens('a {\n}').filter(([, s]) => s === 'bracket').map(([t]) => t)).toEqual(['{', '}'])
    // After the block closes, the next line is an address again.
    expect(styleOf('a {\n\tb\n}\nc.com {\n}', 'c.com')).toBe('heading')
  })

  it('does not take a brace in a placeholder for a block', () => {
    expect(styleOf('a {\n\theader X {host}\n\tbdir\n}\ne.com {\n}', 'bdir')).toBe('keyword')
    expect(styleOf('a {\n\theader X {host}\n\tbdir\n}\ne.com {\n}', 'e.com')).toBe('heading')
  })

  it('keeps a heredoc as a string until its marker, across lines', () => {
    const t = tokens('a {\n\trespond <<EOF\nhello {\nEOF 200\n\tafter\n}')
    expect(t.find(([x]) => x === 'hello {')?.[1]).toBe('string')
    expect(t.find(([x]) => x === 'after')?.[1]).toBe('keyword')
  })

  it('colours import like any directive and its argument as a plain word', () => {
    expect(styleOf('import conf.d/*.caddy', 'import')).toBe('keyword')
  })

  it('reads a top-level import line as a directive, not as an address', () => {
    expect(styleOf('import conf.d/*.caddy\nexample.com {\n}', 'example.com')).toBe('heading')
  })
})

describe('the Caddyfile indentation', () => {
  // The state at the start of the line that follows `text`.
  const after = (text) => {
    const state = lang.startState(2)
    for (const line of text.split('\n')) {
      const stream = new StringStream(line, 4, 2)
      while (!stream.eol()) {
        stream.start = stream.pos
        lang.token(stream, state)
      }
    }
    return state
  }
  const indent = (text, next) => lang.indent(after(text), next, { unit: 1 })

  it('puts a line inside a block one unit in, and nested blocks one more', () => {
    expect(indent('a {', 'b')).toBe(1)
    expect(indent('a {\n\tb {', 'c')).toBe(2)
  })

  it('puts a closing brace back at the level of its block', () => {
    expect(indent('a {\n\tb', '}')).toBe(0)
    expect(indent('a {\n\tb {\n\t\tc', '}')).toBe(1)
  })

  it('does not indent at the top level, or after a block has closed', () => {
    expect(indent('', 'a')).toBe(0)
    expect(indent('a {\n\tb\n}', 'c')).toBe(0)
  })

  it('does not count the braces of a placeholder', () => {
    expect(indent('a {\n\theader X {http.request.host}', 'b')).toBe(1)
  })

  it('leaves the inside of a heredoc alone', () => {
    expect(indent('a {\n\trespond <<EOF', 'text')).toBeNull()
  })
})
