<script>
  import { closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete'
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
  import { HighlightStyle, StreamLanguage, bracketMatching, foldGutter, foldKeymap, foldService, indentOnInput, indentUnit, syntaxHighlighting } from '@codemirror/language'
  import { Annotation, EditorState, RangeSet, StateEffect, StateField } from '@codemirror/state'
  import { Decoration, EditorView, GutterMarker, WidgetType, drawSelection, gutter, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
  import { tags } from '@lezer/highlight'
  import { untrack } from 'svelte'
  import { caddyfileTokenizer } from '../lib/caddyfile-language.js'
  import { blockRanges } from '../lib/blocks.js'
  import { diffLines } from '../lib/diff.js'
  import { cspNonce } from '../lib/nonce.js'

  /** @type {{value: string, label: string, errorLine?: number, errorMessage?: string, baseline?: string, readonly?: boolean, onchange?: (text: string) => void}} */
  let { value, label, errorLine = 0, errorMessage = '', baseline = undefined, readonly = false, onchange = () => {} } = $props()

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

  // What is wrong, written on its own row right under the line, as part of the page and not of the text.
  class ErrorMessage extends WidgetType {
    /** @param {string} text */
    constructor(text) {
      super()
      this.text = text
    }
    eq(other) {
      return other.text === this.text
    }
    toDOM() {
      const el = document.createElement('div')
      el.className = 'cm-error-message'
      el.textContent = this.text
      return el
    }
  }
  const setErrorMessage = StateEffect.define()
  const errorMessageField = StateField.define({
    create: () => '',
    update: (text, tr) => tr.effects.reduce((current, effect) => (effect.is(setErrorMessage) ? effect.value : current), text),
  })
  const errorMessageDecoration = EditorView.decorations.compute([errorLineField, errorMessageField], (state) => {
    const line = state.field(errorLineField)
    const text = state.field(errorMessageField)
    if (!text || line < 1 || line > state.doc.lines) return Decoration.none
    return Decoration.set([Decoration.widget({ widget: new ErrorMessage(text), block: true, side: 1 }).range(state.doc.line(line).to)])
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

  class ChangeMarker extends GutterMarker {
    /** @param {'added'|'removed'} kind */
    constructor(kind) {
      super()
      this.kind = kind
    }
    eq(other) {
      return other.kind === this.kind
    }
    toDOM() {
      const el = document.createElement('span')
      el.className = 'cm-change-marker'
      el.dataset.kind = this.kind
      return el
    }
  }
  const markers = { added: new ChangeMarker('added'), removed: new ChangeMarker('removed') }

  /** The margin marks for the lines that differ from `base`: added or changed lines, and where lines were removed. */
  function changeMarkers(state, base) {
    if (base === undefined || base === state.doc.toString()) return RangeSet.empty
    const marks = new Map()
    let line = 1
    let removedAt = 0
    for (const entry of diffLines(base, state.doc.toString())) {
      if (entry.type === 'same') line++
      else if (entry.type === 'add') marks.set(line++, 'added')
      else removedAt = removedAt || line
      if (entry.type !== 'del' && removedAt) {
        const at = Math.min(removedAt, state.doc.lines)
        if (!marks.has(at)) marks.set(at, 'removed')
        removedAt = 0
      }
    }
    if (removedAt) marks.set(state.doc.lines, marks.get(state.doc.lines) ?? 'removed')
    const ranges = [...marks].sort((a, b) => a[0] - b[0]).map(([n, kind]) => markers[kind].range(state.doc.line(n).from))
    return RangeSet.of(ranges)
  }

  const setBaseline = StateEffect.define()
  const baselineField = StateField.define({
    create: () => undefined,
    update: (base, tr) => tr.effects.reduce((current, effect) => (effect.is(setBaseline) ? effect.value : current), base),
  })
  const changeField = StateField.define({
    create: () => RangeSet.empty,
    update: (marks, tr) =>
      tr.docChanged || tr.effects.some((effect) => effect.is(setBaseline)) ? changeMarkers(tr.state, tr.state.field(baselineField)) : marks,
  })
  const changeGutter = gutter({ class: 'cm-change-gutter', markers: (view) => view.state.field(changeField) })

  // A block folds from the end of the line that opens it to its closing brace, which stays in view.
  const folding = foldService.of((state, lineStart) => {
    const startLine = state.doc.lineAt(lineStart)
    const range = blockRanges(state.doc.toString())
      .filter((r) => r.startLine === startLine.number)
      .reduce((widest, r) => (!widest || r.endLine > widest.endLine ? r : widest), null)
    return range ? { from: startLine.to, to: state.doc.line(range.endLine).from + range.endColumn } : null
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
    '&': { color: 'var(--text)', backgroundColor: 'transparent', fontSize: '0.9rem', height: '100%' },
    '&.cm-focused': { outline: '2px solid var(--accent)', outlineOffset: '-1px' },
    '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.6', overflow: 'auto' },
    '.cm-content': { caretColor: 'var(--accent)', padding: '0.5rem 0' },
    '.cm-cursor': { borderLeftColor: 'var(--accent)' },
    '.cm-gutters': { backgroundColor: 'transparent', color: 'var(--faint)', border: 'none' },
    '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
    '.cm-activeLineGutter': { backgroundColor: 'color-mix(in srgb, var(--accent) 10%, transparent)', color: 'var(--text)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': {
      backgroundColor: 'color-mix(in srgb, var(--accent) 25%, transparent)',
    },
    '.cm-matchingBracket': { backgroundColor: 'color-mix(in srgb, var(--accent) 22%, transparent)', outline: '1px solid var(--accent)', color: 'inherit' },
    '.cm-nonmatchingBracket': { backgroundColor: 'color-mix(in srgb, var(--bad) 25%, transparent)', color: 'inherit' },
    '.cm-error-line': { backgroundColor: 'var(--bad-soft)' },
    '.cm-error-message': { padding: '0.15rem 0.5rem 0.4rem 1rem', backgroundColor: 'var(--bad-soft)', color: 'var(--bad)', fontFamily: 'var(--sans)', fontSize: '0.85rem', fontWeight: '500' },
    '.cm-error-gutter': { minWidth: '1.1rem' },
    '.cm-change-gutter': { minWidth: '0.4rem' },
    '.cm-change-marker': { display: 'block', height: '100%', width: '3px', marginLeft: '2px' },
    '.cm-change-marker[data-kind="added"]': { backgroundColor: 'var(--accent)' },
    '.cm-change-marker[data-kind="removed"]': { backgroundColor: 'var(--accent)', height: '3px', marginTop: '-1px' },
    '.cm-foldGutter span': { cursor: 'pointer', color: 'var(--muted)', padding: '0 0.2rem' },
    '.cm-foldPlaceholder': { backgroundColor: 'var(--surface-2)', color: 'var(--muted)', border: '1px solid var(--border, var(--muted))', padding: '0 0.3rem' },
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
        changeGutter,
        baselineField,
        changeField,
        folding,
        foldGutter(),
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
        errorMessageField,
        errorMessageDecoration,
        theme,
        // Fixed when the editor is made: a page shows the file either to edit or to read.
        EditorState.readOnly.of(untrack(() => readonly)),
        EditorView.editable.of(!untrack(() => readonly)),
        EditorView.cspNonce.of(cspNonce()),
        keymap.of([...closeBracketsKeymap, ...foldKeymap, ...defaultKeymap, ...historyKeymap, indentWithTab]),
        EditorView.contentAttributes.of({ 'aria-label': untrack(() => label) }),
        EditorView.updateListener.of((update) => {
          if (update.docChanged && !update.transactions.some((tr) => tr.annotation(fromOutside))) onchange(update.state.doc.toString())
        }),
      ],
    })
    view = new EditorView({ state, parent: host })
    view.dispatch({ effects: [setErrorLine.of(untrack(() => errorLine)), setErrorMessage.of(untrack(() => errorMessage)), setBaseline.of(untrack(() => baseline))] })
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
    const base = baseline
    if (view && view.state.field(baselineField) !== base) view.dispatch({ effects: setBaseline.of(base) })
  })

  $effect(() => {
    const text = errorMessage
    if (view && view.state.field(errorMessageField) !== text) view.dispatch({ effects: setErrorMessage.of(text) })
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
