import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startPolling } from './poller.js'

// A stand-in for document: only `hidden` and the visibilitychange event matter.
function fakeDoc(hidden = false) {
  const doc = new EventTarget()
  doc.hidden = hidden
  doc.show = () => {
    doc.hidden = false
    doc.dispatchEvent(new Event('visibilitychange'))
  }
  return doc
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('startPolling', () => {
  it('runs at once and then every interval', async () => {
    const fn = vi.fn().mockResolvedValue()
    const stop = startPolling(fn, 5000, fakeDoc())
    expect(fn).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5000)
    expect(fn).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(10000)
    expect(fn).toHaveBeenCalledTimes(4)
    stop()
  })

  it('stops for good when asked', async () => {
    const fn = vi.fn().mockResolvedValue()
    const stop = startPolling(fn, 1000, fakeDoc())
    stop()
    await vi.advanceTimersByTimeAsync(10000)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('skips ticks while the page is hidden and runs again as soon as it is shown', async () => {
    const doc = fakeDoc(true)
    const fn = vi.fn().mockResolvedValue()
    const stop = startPolling(fn, 1000, doc)
    expect(fn).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(5000)
    expect(fn).not.toHaveBeenCalled()

    doc.show()
    expect(fn).toHaveBeenCalledTimes(1)
    stop()
  })

  it('does not start a call while the previous one is still running', async () => {
    let finish
    const fn = vi.fn(() => new Promise((resolve) => (finish = resolve)))
    const stop = startPolling(fn, 1000, fakeDoc())

    await vi.advanceTimersByTimeAsync(5000)
    expect(fn).toHaveBeenCalledTimes(1)

    finish()
    await vi.advanceTimersByTimeAsync(1000)
    expect(fn).toHaveBeenCalledTimes(2)
    stop()
  })

  it('keeps polling after a call fails', async () => {
    const fn = vi.fn().mockRejectedValueOnce(new Error('boom')).mockResolvedValue()
    const stop = startPolling(fn, 1000, fakeDoc())

    await vi.advanceTimersByTimeAsync(2000)
    expect(fn).toHaveBeenCalledTimes(3)
    stop()
  })
})
