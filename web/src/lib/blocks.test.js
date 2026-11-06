import { describe, expect, it } from 'vitest'
import { blockRanges } from './blocks.js'

describe('blockRanges', () => {
  it('finds a block from the line that opens it to the line that closes it', () => {
    expect(blockRanges('a {\n\tb\n}')).toEqual([{ startLine: 1, endLine: 3, endColumn: 0 }])
  })

  it('reports the column of the closing brace, after its indentation', () => {
    expect(blockRanges('a {\n\tb {\n\t\tc\n\t}\n}')).toEqual([
      { startLine: 2, endLine: 4, endColumn: 1 },
      { startLine: 1, endLine: 5, endColumn: 0 },
    ])
  })

  it('leaves out a block that opens and closes on one line', () => {
    expect(blockRanges('a { b }\nc')).toEqual([])
  })

  it('does not take a placeholder for a block', () => {
    expect(blockRanges('a {\n\trespond {http.request.uri}\n}')).toEqual([{ startLine: 1, endLine: 3, endColumn: 0 }])
  })

  it('does not count braces in comments, strings or heredocs', () => {
    expect(blockRanges('# {\na {\n\trespond "}"\n\tx <<EOT\n}\nEOT\n}')).toEqual([{ startLine: 2, endLine: 7, endColumn: 0 }])
  })

  it('leaves out a block that is never closed, and a stray closing brace', () => {
    expect(blockRanges('a {\n\tb')).toEqual([])
    expect(blockRanges('}\na')).toEqual([])
  })

  it('is empty for an empty text', () => {
    expect(blockRanges('')).toEqual([])
  })
})
