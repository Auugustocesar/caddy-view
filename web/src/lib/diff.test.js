import { describe, expect, it } from 'vitest'
import { diffLines, withContext } from './diff.js'

const same = (text) => ({ type: 'same', text })
const add = (text) => ({ type: 'add', text })
const del = (text) => ({ type: 'del', text })

describe('diffLines', () => {
  it('has only unchanged lines for equal texts', () => {
    expect(diffLines('a\nb\nc', 'a\nb\nc')).toEqual([same('a'), same('b'), same('c')])
  })

  it('has nothing for two empty texts', () => {
    expect(diffLines('', '')).toEqual([])
  })

  it('marks a line that was added', () => {
    expect(diffLines('a\nc', 'a\nb\nc')).toEqual([same('a'), add('b'), same('c')])
  })

  it('marks a line that was removed', () => {
    expect(diffLines('a\nb\nc', 'a\nc')).toEqual([same('a'), del('b'), same('c')])
  })

  it('shows a changed line as the old one removed and the new one added', () => {
    expect(diffLines('a\nb\nc', 'a\nx\nc')).toEqual([same('a'), del('b'), add('x'), same('c')])
  })

  it('keeps lines added at either end', () => {
    expect(diffLines('b', 'a\nb\nc')).toEqual([add('a'), same('b'), add('c')])
  })

  it('shows everything added to an empty text and everything removed to an empty one', () => {
    expect(diffLines('', 'a\nb')).toEqual([del(''), add('a'), add('b')])
    expect(diffLines('a\nb', '')).toEqual([del('a'), del('b'), add('')])
  })

  it('does not mistake a trailing newline for a line of its own that changed', () => {
    expect(diffLines('a\n', 'a\n')).toEqual([same('a'), same('')])
  })

  it('treats CRLF like LF', () => {
    expect(diffLines('a\r\nb', 'a\nb')).toEqual([same('a'), same('b')])
  })

  it('finds the common lines in the middle of a larger change', () => {
    const before = ['one', 'two', 'three', 'four', 'five'].join('\n')
    const after = ['one', 'TWO', 'three', 'four', 'FIVE', 'six'].join('\n')
    expect(diffLines(before, after)).toEqual([
      same('one'),
      del('two'),
      add('TWO'),
      same('three'),
      same('four'),
      del('five'),
      add('FIVE'),
      add('six'),
    ])
  })

  it('does not take forever on large texts, and says so with a plain replacement', () => {
    const big = (tag) => Array.from({ length: 5000 }, (_, i) => `${tag} ${i}`).join('\n')
    const result = diffLines(big('a'), big('b'))
    expect(result.filter((l) => l.type === 'del')).toHaveLength(5000)
    expect(result.filter((l) => l.type === 'add')).toHaveLength(5000)
  })
})

describe('withContext', () => {
  const run = (n, tag = 's') => Array.from({ length: n }, (_, i) => same(`${tag}${i}`))

  it('has nothing to show when nothing changed', () => {
    expect(withContext(run(5), 2)).toEqual([])
  })

  it('keeps the context around a change and hides the rest', () => {
    const lines = [...run(6, 'a'), add('x'), ...run(6, 'b')]
    expect(withContext(lines, 2)).toEqual([
      { type: 'skip', count: 4 },
      same('a4'),
      same('a5'),
      add('x'),
      same('b0'),
      same('b1'),
      { type: 'skip', count: 4 },
    ])
  })

  it('shows a short run between two changes whole', () => {
    const lines = [del('x'), ...run(4, 'm'), add('y')]
    expect(withContext(lines, 2)).toEqual([del('x'), ...run(4, 'm'), add('y')])
  })

  it('hides a long run between two changes', () => {
    const lines = [del('x'), ...run(7, 'm'), add('y')]
    expect(withContext(lines, 2)).toEqual([del('x'), same('m0'), same('m1'), { type: 'skip', count: 3 }, same('m5'), same('m6'), add('y')])
  })

  it('shows changes at the very start and end without a skip', () => {
    expect(withContext([add('x'), ...run(2)], 2)).toEqual([add('x'), same('s0'), same('s1')])
    expect(withContext([...run(2), add('x')], 2)).toEqual([same('s0'), same('s1'), add('x')])
  })
})
