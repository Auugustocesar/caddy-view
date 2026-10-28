// jsdom has no layout, so it has no ResizeObserver either: a component that measures itself (the chart) gets one that
// never reports, and falls back to its default size.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// CodeMirror measures text with Range rectangles, which jsdom does not implement: an empty answer is enough.
const noRects = Object.assign([], { item: () => null })
Range.prototype.getClientRects ??= () => noRects
Range.prototype.getBoundingClientRect ??= () => new DOMRect(0, 0, 0, 0)
