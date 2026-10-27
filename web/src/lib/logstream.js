import { parseLogLine } from './logline.js'

/** @typedef {'connecting'|'live'|'reconnecting'|'unavailable'|'closed'} StreamStatus */
/** @typedef {import('./logline.js').LogLine & {id: number}} StreamLine */

const DEFAULT_MAX = 1000

/** Follows the log stream route and keeps the newest lines. */
export class LogStream {
  /** @type {StreamStatus} */
  status = 'connecting'
  /** Why the stream cannot run, when `status` is `unavailable`. */
  reason = ''
  /** @type {StreamLine[]} */
  lines = []
  paused = false
  /** How many lines arrived while paused and are not shown yet. */
  heldCount = 0

  #url
  #max
  #onChange
  #EventSource
  #fetch
  /** @type {EventSource|null} */
  #source = null
  /** @type {StreamLine[]} */
  #held = []
  #seq = 0

  /**
   * @param {string} url
   * @param {{max?: number, onChange?: () => void, EventSourceImpl?: typeof EventSource, fetchImpl?: typeof fetch}} [options]
   */
  constructor(url, options = {}) {
    this.#url = url
    this.#max = options.max ?? DEFAULT_MAX
    this.#onChange = options.onChange ?? (() => {})
    this.#EventSource = options.EventSourceImpl ?? globalThis.EventSource
    this.#fetch = options.fetchImpl ?? globalThis.fetch
  }

  /** Opens the connection. Does nothing when it is already open. */
  start() {
    if (this.#source) return
    this.status = 'connecting'
    this.reason = ''
    const source = new this.#EventSource(this.#url)
    this.#source = source

    // Events from a source that was replaced or stopped must not touch the state.
    const current = (fn) => (event) => {
      if (this.#source === source) fn(event)
    }
    source.addEventListener('open', current(() => this.#set('live')))
    source.addEventListener('message', current((e) => this.#add(e.data)))
    source.addEventListener(
      'error',
      current(() => {
        // While the browser is still retrying, it reconnects by itself. Once it has given up, the server's own
        // answer is the only way to learn why.
        if (source.readyState === this.#EventSource.CLOSED) {
          this.#explain(source)
        } else {
          this.#set('reconnecting')
        }
      }),
    )
  }

  /** Closes the connection. Lines already received stay. */
  stop() {
    this.#source?.close()
    this.#source = null
    this.#set('closed')
  }

  pause() {
    this.paused = true
    this.#onChange()
  }

  resume() {
    this.paused = false
    this.lines = this.lines.concat(this.#held).slice(-this.#max)
    this.#held = []
    this.heldCount = 0
    this.#onChange()
  }

  clear() {
    this.lines = []
    this.#held = []
    this.heldCount = 0
    this.#onChange()
  }

  /** @param {StreamStatus} status */
  #set(status) {
    this.status = status
    if (status === 'live') this.reason = ''
    this.#onChange()
  }

  /** @param {string} data */
  #add(data) {
    const line = { id: ++this.#seq, ...parseLogLine(data) }
    if (this.paused) {
      this.#held = [...this.#held, line].slice(-this.#max)
      this.heldCount = this.#held.length
    } else {
      this.lines = [...this.lines, line].slice(-this.#max)
    }
    this.#onChange()
  }

  /** @param {EventSource} source */
  async #explain(source) {
    const reason = await this.#askServer()
    if (this.#source !== source) return // stopped while asking
    this.status = 'unavailable'
    this.reason = reason
    this.#onChange()
  }

  /** @returns {Promise<string>} */
  async #askServer() {
    const abort = new AbortController()
    const request = this.#fetch // called as a plain function: window.fetch refuses another receiver
    try {
      const res = await request(this.#url, { signal: abort.signal, headers: { Accept: 'application/json' } })
      if (res.ok) {
        // It is streaming after all, so there is no error to read. Do not hold this second stream open.
        await res.body?.cancel()
        return 'the log stream was closed'
      }
      const body = await res.json().catch(() => null)
      return typeof body?.error === 'string' ? body.error : `the server answered HTTP ${res.status}`
    } catch {
      return 'cannot reach caddy-view'
    } finally {
      abort.abort()
    }
  }
}
