// jsdom has no layout, so it has no ResizeObserver either: a component that measures itself (the chart) gets one that
// never reports, and falls back to its default size.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}
