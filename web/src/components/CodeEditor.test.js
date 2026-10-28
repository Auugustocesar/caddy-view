import { insertNewlineAndIndent } from '@codemirror/commands'
import { indentUnit } from '@codemirror/language'
import { EditorView } from '@codemirror/view'
import { render } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import CodeEditor from './CodeEditor.svelte'

const viewOf = (container) => EditorView.findFromDOM(container.querySelector('.cm-editor'))
const textOf = (container) => viewOf(container).state.doc.toString()

describe('CodeEditor', () => {
  it('shows the text with line numbers', () => {
    const { container } = render(CodeEditor, { value: 'a {\n\tb\n}', label: 'Caddyfile' })
    expect(textOf(container)).toBe('a {\n\tb\n}')
    expect(container.querySelector('.cm-lineNumbers')).not.toBeNull()
    expect(container.querySelectorAll('.cm-line')).toHaveLength(3)
  })

  it('is named for screen readers', () => {
    const { container } = render(CodeEditor, { value: 'a', label: 'Caddyfile' })
    expect(container.querySelector('.cm-content').getAttribute('aria-label')).toBe('Caddyfile')
  })

  it('indents with tabs, as caddy fmt does', () => {
    const { container } = render(CodeEditor, { value: 'a', label: 'Caddyfile' })
    expect(viewOf(container).state.facet(indentUnit)).toBe('\t')
  })

  it('follows the value it is given', async () => {
    const { container, rerender } = render(CodeEditor, { value: 'one', label: 'Caddyfile' })
    await rerender({ value: 'two\nlines', label: 'Caddyfile' })
    expect(textOf(container)).toBe('two\nlines')
  })

  it('does not report a change it was told about', async () => {
    const onchange = vi.fn()
    const { rerender } = render(CodeEditor, { value: 'one', label: 'Caddyfile', onchange })
    await rerender({ value: 'two', label: 'Caddyfile', onchange })
    expect(onchange).not.toHaveBeenCalled()
  })

  it('reports an edit with the whole new text', () => {
    const onchange = vi.fn()
    const { container } = render(CodeEditor, { value: 'abc', label: 'Caddyfile', onchange })
    viewOf(container).dispatch({ changes: { from: 3, insert: 'd' } })
    expect(onchange).toHaveBeenCalledExactlyOnceWith('abcd')
  })

  it('keeps the text the user typed when the value it was given has not changed', async () => {
    const { container, rerender } = render(CodeEditor, { value: 'abc', label: 'Caddyfile', errorLine: 0 })
    viewOf(container).dispatch({ changes: { from: 3, insert: 'd' } })
    await rerender({ value: 'abc', label: 'Caddyfile', errorLine: 1 })
    expect(textOf(container)).toBe('abcd')
  })

  it('marks the line of an error, in the text and in the margin', () => {
    const { container } = render(CodeEditor, { value: 'a {\n\tnope\n}', label: 'Caddyfile', errorLine: 2 })
    const marked = container.querySelectorAll('.cm-line.cm-error-line')
    expect(marked).toHaveLength(1)
    expect(marked[0].textContent).toBe('\tnope')
    expect(container.querySelectorAll('.cm-error-marker')).toHaveLength(1)
  })

  it('moves the mark when the error moves, and removes it when the error goes', async () => {
    const { container, rerender } = render(CodeEditor, { value: 'a\nb\nc', label: 'Caddyfile', errorLine: 1 })
    await rerender({ value: 'a\nb\nc', label: 'Caddyfile', errorLine: 3 })
    expect(container.querySelector('.cm-error-line').textContent).toBe('c')
    await rerender({ value: 'a\nb\nc', label: 'Caddyfile', errorLine: 0 })
    expect(container.querySelector('.cm-error-line')).toBeNull()
    expect(container.querySelector('.cm-error-marker')).toBeNull()
  })

  it('ignores an error line the text does not have', () => {
    const { container } = render(CodeEditor, { value: 'a\nb', label: 'Caddyfile', errorLine: 9 })
    expect(container.querySelector('.cm-error-line')).toBeNull()
  })

  it('highlights the pair of a brace next to the cursor', () => {
    const { container } = render(CodeEditor, { value: 'a {\n\tb\n}', label: 'Caddyfile' })
    viewOf(container).dispatch({ selection: { anchor: 3 } })
    expect(container.querySelectorAll('.cm-matchingBracket')).toHaveLength(2)
  })

  it('highlights the pair of a parenthesis, as in a snippet name', () => {
    const { container } = render(CodeEditor, { value: '(log) {\n}', label: 'Caddyfile' })
    viewOf(container).dispatch({ selection: { anchor: 1 } })
    expect(container.querySelectorAll('.cm-matchingBracket')).toHaveLength(2)
  })

  it('gives each kind of token its own class so it can be coloured', () => {
    const { container } = render(CodeEditor, { value: '# note\nexample.com {\n\trespond "hi"\n}', label: 'Caddyfile' })
    const classes = new Set(Array.from(container.querySelectorAll('.cm-line span[class]')).map((s) => s.className))
    expect(classes.size).toBeGreaterThanOrEqual(3)
  })

  it('removes the editor when it goes away', () => {
    const { container, unmount } = render(CodeEditor, { value: 'a', label: 'Caddyfile' })
    const view = viewOf(container)
    unmount()
    expect(container.querySelector('.cm-editor')).toBeNull()
    expect(() => view.state).not.toThrow()
  })
})

describe('CodeEditor brackets', () => {
  it('is told which characters to close: braces, parentheses and both kinds of quote', () => {
    const { container } = render(CodeEditor, { value: 'a', label: 'Caddyfile' })
    const [config] = viewOf(container).state.languageDataAt('closeBrackets', 0)
    expect(config?.brackets).toEqual(['(', '{', '"', '`'])
  })

  it('closes a brace the user types, leaving the cursor between the two', () => {
    const { container } = render(CodeEditor, { value: '', label: 'Caddyfile' })
    const view = viewOf(container)
    // closeBrackets answers the input handler, which is what a typed character goes through.
    const handled = view.state.facet(EditorView.inputHandler).some((handler) => handler(view, 0, 0, '{', () => view.state.update({ changes: { from: 0, insert: '{' } })))
    expect(handled).toBe(true)
    expect(view.state.doc.toString()).toBe('{}')
    expect(view.state.selection.main.head).toBe(1)
  })
})

describe('CodeEditor indentation', () => {
  const enterAfter = (text) => {
    const { container } = render(CodeEditor, { value: text, label: 'Caddyfile' })
    const view = viewOf(container)
    view.dispatch({ selection: { anchor: text.length } })
    insertNewlineAndIndent(view)
    return view.state.doc.toString()
  }

  it('indents the line after an opening brace, with a tab', () => {
    expect(enterAfter('example.com {')).toBe('example.com {\n\t')
    expect(enterAfter('example.com{')).toBe('example.com{\n\t')
  })

  it('indents one more inside a nested block', () => {
    expect(enterAfter('a {\n\tb {')).toBe('a {\n\tb {\n\t\t')
  })

  it('keeps the indentation of the line above inside a block', () => {
    expect(enterAfter('a {\n\trespond "x"')).toBe('a {\n\trespond "x"\n\t')
  })

  it('does not indent at the top level', () => {
    expect(enterAfter('a {\n}')).toBe('a {\n}\n')
  })
})
