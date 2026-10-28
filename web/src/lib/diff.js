/**
 * @typedef {{type: 'same'|'add'|'del', text: string}} DiffLine
 */

// The longest texts that get a line-by-line diff: the table is lines x lines. Beyond it the whole of one text is shown
// as replaced by the other, which is true, only less precise.
const MAX_CELLS = 4_000_000

const lines = (text) => text.replaceAll('\r\n', '\n').split('\n')

/** The lines that differ between two texts, in order. @param {string} before @param {string} after @returns {DiffLine[]} */
export function diffLines(before, after) {
  if (before === '' && after === '') return []
  const a = lines(before)
  const b = lines(after)

  // What the two texts start and end with is the same by definition; only the middle needs work.
  let start = 0
  while (start < a.length && start < b.length && a[start] === b[start]) start++
  let endA = a.length
  let endB = b.length
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--
    endB--
  }

  const head = a.slice(0, start).map((text) => ({ type: /** @type {const} */ ('same'), text }))
  const tail = a.slice(endA).map((text) => ({ type: /** @type {const} */ ('same'), text }))
  return [...head, ...diffMiddle(a.slice(start, endA), b.slice(start, endB)), ...tail]
}

/** @param {string[]} a @param {string[]} b @returns {DiffLine[]} */
function diffMiddle(a, b) {
  if (a.length === 0 || b.length === 0 || a.length * b.length > MAX_CELLS) {
    return [...a.map((text) => ({ type: /** @type {const} */ ('del'), text })), ...b.map((text) => ({ type: /** @type {const} */ ('add'), text }))]
  }

  // lcs[i][j] is the length of the longest common run of lines in a[i:] and b[j:].
  const lcs = Array.from({ length: a.length + 1 }, () => new Uint32Array(b.length + 1))
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }

  /** @type {DiffLine[]} */
  const out = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ type: 'same', text: a[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ type: 'del', text: a[i++] })
    } else {
      out.push({ type: 'add', text: b[j++] })
    }
  }
  while (i < a.length) out.push({ type: 'del', text: a[i++] })
  while (j < b.length) out.push({ type: 'add', text: b[j++] })
  return out
}

/**
 * The changed lines with `context` unchanged lines around each; a longer run of unchanged lines becomes one `skip`
 * entry that says how many it hides. A diff without changes is an empty list.
 * @param {DiffLine[]} diff @param {number} context
 * @returns {Array<DiffLine | {type: 'skip', count: number}>}
 */
export function withContext(diff, context) {
  if (!diff.some((line) => line.type !== 'same')) return []

  const keep = new Array(diff.length).fill(false)
  diff.forEach((line, i) => {
    if (line.type === 'same') return
    for (let k = Math.max(0, i - context); k <= Math.min(diff.length - 1, i + context); k++) keep[k] = true
  })

  /** @type {Array<DiffLine | {type: 'skip', count: number}>} */
  const out = []
  let hidden = 0
  diff.forEach((line, i) => {
    if (keep[i]) {
      if (hidden > 0) out.push({ type: 'skip', count: hidden })
      hidden = 0
      out.push(line)
    } else {
      hidden++
    }
  })
  if (hidden > 0) out.push({ type: 'skip', count: hidden })
  return out
}
