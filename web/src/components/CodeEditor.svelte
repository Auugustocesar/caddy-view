<script>
  import { closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete'
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
  import { HighlightStyle, StreamLanguage, bracketMatching, indentOnInput, indentUnit, syntaxHighlighting } from '@codemirror/language'
  import { Annotation, EditorState, StateEffect, StateField } from '@codemirror/state'
  import { Decoration, EditorView, GutterMarker, drawSelection, gutter, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
  import { tags } from '@lezer/highlight'
  import { untrack } from 'svelte'
  import { caddyfileTokenizer } from '../lib/caddyfile-language.js'
  import { cspNonce } from '../lib/nonce.js'

  /** @type {{value: string, label: string, errorLine?: number, readonly?: boolean, onchange?: (text: string) => void}} */
  let { value, label, errorLine = 0, readonly = false, onchange = () => {} } = $props()

  // Changes the page makes (a new value, a formatted text) are not edits and are not reported.
  const fromOutside = Annotation.define()

  const setErrorLine = StateEffect.define()
  const errorLineField = StateField.define({
    create: () => 0,
    update: (line, tr) => tr.effects.reduce((current, effect) => (effect.is(setErrorLine) ? effect.value : current), line),
    provide: (field) =>
      EditorView.decorations.from(field, (line) => ({ state }) =>
        line >= 1 && line <= state.doc.lines ? Decoration.set([Decoration.line({ class: 'cm-error-line' }).range(state.doc.line(line).from)]) : Decoration.none,
      ),
  })

  class ErrorMarker extends GutterMarker {
    toDOM() {
      const el = document.createElement('span')
      el.className = 'cm-error-marker'
      el.textContent = '!'
      return el
    }
  }
  const errorMarker = new ErrorMarker()
  const errorGutter = gutter({
    class: 'cm-error-gutter',
    lineMarker: (view, line) => (view.state.field(errorLineField) === view.state.doc.lineAt(line.from).number ? errorMarker : null),
    lineMarkerChange: (update) => update.startState.field(errorLineField) !== update.state.field(errorLineField),
  })

  // The colours are custom properties of the design system, so the editor follows the theme with no extra work.
  const highlighting = HighlightStyle.define([
    { tag: tags.comment, color: 'var(--syn-comment)', fontStyle: 'italic' },
    { tag: tags.string, color: 'var(--syn-string)' },
    { tag: tags.keyword, color: 'var(--syn-keyword)' },
    { tag: tags.heading, color: 'var(--syn-heading)', fontWeight: '600' },
    { tag: tags.variableName, color: 'var(--syn-variable)' },
    { tag: tags.labelName, color: 'var(--syn-label)' },
    { tag: tags.typeName, color: 'var(--syn-type)' },
    { tag: tags.bracket, color: 'var(--muted)' },
  ])

  const theme = EditorView.theme({
    '&': { color: 'var(--text)', backgroundColor: 'var(--surface)', fontSize: '0.9rem', height: '100%' },
    '&.cm-focused': { outline: '2px solid var(--accent)', outlineOffset: '-1px' },
    '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.6', overflow: 'auto' },
    '.cm-content': { caretColor: 'var(--accent)', padding: '0.5rem 0' },
    '.cm-cursor': { borderLeftColor: 'var(--accent)' },
    '.cm-gutters': { backgroundColor: 'var(--surface-2)', color: 'var(--muted)', border: 'none' },
    '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
    '.cm-activeLineGutter': { backgroundColor: 'color-mix(in srgb, var(--accent) 10%, transparent)', color: 'var(--text)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': {
      backgroundColor: 'color-mix(in srgb, var(--accent) 25%, transparent)',
    },
    '.cm-matchingBracket': { backgroundColor: 'color-mix(in srgb, var(--accent) 22%, transparent)', outline: '1px solid var(--accent)', color: 'inherit' },
    '.cm-nonmatchingBracket': { backgroundColor: 'color-mix(in srgb, var(--bad) 25%, transparent)', color: 'inherit' },
    '.cm-error-line': { backgroundColor: 'color-mix(in srgb, var(--bad) 14%, transparent)', boxShadow: 'inset 3px 0 0 var(--bad)' },
    '.cm-error-gutter': { minWidth: '1.1rem' },
    '.cm-error-marker': { color: 'var(--bad)', fontWeight: '700' },
  })

  /** @type {HTMLElement} */
  let host
  /** @type {EditorView | null} */
  let view = null

  $effect(() => {
    const state = EditorState.create({
      doc: untrack(() => value),
      extensions: [
        lineNumbers(),
        errorGutter,
        highlightActiveLineGutter(),
        highlightActiveLine(),
        history(),
        drawSelection(),
        bracketMatching(),
        closeBrackets(),
        indentOnInput(),
        indentUnit.of('\t'),
        EditorState.tabSize.of(4),
        StreamLanguage.define(caddyfileTokenizer),
        syntaxHighlighting(highlighting),
        errorLineField,
        theme,
        // Fixed when the editor is made: a page shows the file either to edit or to read.
        EditorState.readOnly.of(untrack(() => readonly)),
        EditorView.editable.of(!untrack(() => readonly)),
        EditorView.cspNonce.of(cspNonce()),
        keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, indentWithTab]),
        EditorView.contentAttributes.of({ 'aria-label': untrack(() => label) }),
        EditorView.updateListener.of((update) => {
          if (update.docChanged && !update.transactions.some((tr) => tr.annotation(fromOutside))) onchange(update.state.doc.toString())
        }),
      ],
    })
    view = new EditorView({ state, parent: host })
    view.dispatch({ effects: setErrorLine.of(untrack(() => errorLine)) })
    return () => {
      view?.destroy()
      view = null
    }
  })

  // What the page last asked the editor to show. The editor follows a new value, not the page being rendered again with
  // the one it had: whatever has been typed since stays.
  let given = untrack(() => value)
  $effect(() => {
    const text = value
    if (text === given) return
    given = text
    if (view && text !== view.state.doc.toString()) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text }, annotations: fromOutside.of(true) })
    }
  })

  $effect(() => {
    const line = errorLine
    if (view && view.state.field(errorLineField) !== line) view.dispatch({ effects: setErrorLine.of(line) })
  })
</script>

<div class="editor" bind:this={host}></div>

<style>
  .editor {
    height: 100%;
    min-height: 16rem;
    border-radius: var(--radius);
    overflow: hidden;
  }
</style>
