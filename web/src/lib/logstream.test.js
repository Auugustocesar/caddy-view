import { beforeEach, describe, expect, it, vi } from 'vitest'
import { LogStream } from './logstream.js'

/** An EventSource that the test drives by hand. */
class FakeEventSource extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  static instances = []

  constructor(url) {
    super()
    this.url = url
    this.readyState = FakeEventSource.CONNECTING
    this.closed = false
    FakeEventSource.instances.push(this)
  }

  close() {
    this.closed = true
    this.readyState = FakeEventSource.CLOSED
  }

  open() {
    this.readyState = FakeEventSource.OPEN
    this.dispatchEvent(new Event('open'))
  }

  message(data) {
    this.dispatchEvent(new MessageEvent('message', { data }))
  }

  fail(readyState) {
    this.readyState = readyState
    this.dispatchEvent(new Event('error'))
  }
}

const STREAM_URL = '/api/logs/stream?backlog=100'

function json(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function setup(options = {}) {
  const onChange = vi.fn()
  const fetchImpl = options.fetchImpl ?? vi.fn().mockResolvedValue(json(200, {}))
  const stream = new LogStream(STREAM_URL, { onChange, EventSourceImpl: FakeEventSource, fetchImpl, ...options })
  stream.start()
  return { stream, es: FakeEventSource.instances.at(-1), onChange, fetchImpl }
}

beforeEach(() => {
  FakeEventSource.instances = []
})

describe('LogStream', () => {
  it('connects to the given URL and starts out connecting', () => {
    const { stream, es } = setup()
    expect(es.url).toBe(STREAM_URL)
    expect(stream.status).toBe('connecting')
    expect(stream.lines).toEqual([])
  })

  it('does not open a second connection when started twice', () => {
    const { stream } = setup()
    stream.start()
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('is live once the connection opens', () => {
    const { stream, es, onChange } = setup()
    es.open()
    expect(stream.status).toBe('live')
    expect(onChange).toHaveBeenCalled()
  })

  it('adds each message as a parsed line with an increasing id', () => {
    const { stream, es, onChange } = setup()
    es.open()
    onChange.mockClear()
    es.message('{"status":404,"request":{"method":"GET","host":"a.example.com","uri":"/x"}}')
    es.message('plain text')

    expect(stream.lines).toHaveLength(2)
    expect(stream.lines[0]).toMatchObject({ status: 404, method: 'GET', host: 'a.example.com', uri: '/x' })
    expect(stream.lines[1]).toMatchObject({ raw: 'plain text', status: null })
    expect(stream.lines[1].id).toBeGreaterThan(stream.lines[0].id)
    expect(onChange).toHaveBeenCalledTimes(2)
  })

  it('keeps only the newest lines', () => {
    const { stream, es } = setup({ max: 3 })
    es.open()
    for (const n of [1, 2, 3, 4, 5]) es.message(`line ${n}`)
    expect(stream.lines.map((l) => l.raw)).toEqual(['line 3', 'line 4', 'line 5'])
  })

  it('shows reconnecting while the browser retries, and live again after it succeeds', () => {
    const { stream, es } = setup()
    es.open()
    es.fail(FakeEventSource.CONNECTING)
    expect(stream.status).toBe('reconnecting')
    es.open()
    expect(stream.status).toBe('live')
  })

  it('keeps the lines it already has while reconnecting', () => {
    const { stream, es } = setup()
    es.open()
    es.message('before')
    es.fail(FakeEventSource.CONNECTING)
    expect(stream.lines.map((l) => l.raw)).toEqual(['before'])
  })

  describe('when the browser gives up', () => {
    it('explains why using the error message of the server', async () => {
      const fetchImpl = vi.fn().mockResolvedValue(json(404, { error: 'no access log found: pass --access-log' }))
      const { stream, es, onChange } = setup({ fetchImpl })
      es.fail(FakeEventSource.CLOSED)
      await vi.waitFor(() => expect(stream.status).toBe('unavailable'))

      expect(stream.reason).toBe('no access log found: pass --access-log')
      expect(fetchImpl.mock.calls[0][0]).toBe(STREAM_URL)
      expect(onChange).toHaveBeenCalled()
    })

    it('uses a generic reason when the server cannot be asked', async () => {
      const fetchImpl = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
      const { stream, es } = setup({ fetchImpl })
      es.fail(FakeEventSource.CLOSED)
      await vi.waitFor(() => expect(stream.status).toBe('unavailable'))
      expect(stream.reason).not.toBe('')
    })

    it('uses a generic reason when the server answers but gives no JSON error', async () => {
      const fetchImpl = vi.fn().mockResolvedValue(new Response('<html>', { status: 500 }))
      const { stream, es } = setup({ fetchImpl })
      es.fail(FakeEventSource.CLOSED)
      await vi.waitFor(() => expect(stream.status).toBe('unavailable'))
      expect(stream.reason).toContain('500')
    })

    it('never opens a second stream while finding out why, and gives up on it', async () => {
      const cancel = vi.fn()
      const body = new ReadableStream({ cancel })
      const fetchImpl = vi.fn().mockResolvedValue(new Response(body, { status: 200 }))
      const { stream, es } = setup({ fetchImpl })
      es.fail(FakeEventSource.CLOSED)
      await vi.waitFor(() => expect(stream.status).toBe('unavailable'))
      await vi.waitFor(() => expect(cancel).toHaveBeenCalled())
    })
  })

  describe('pausing', () => {
    it('freezes the visible lines, counts the new ones, and shows them on resume', () => {
      const { stream, es } = setup()
      es.open()
      es.message('a')
      stream.pause()
      es.message('b')
      es.message('c')

      expect(stream.paused).toBe(true)
      expect(stream.lines.map((l) => l.raw)).toEqual(['a'])
      expect(stream.heldCount).toBe(2)

      stream.resume()
      expect(stream.paused).toBe(false)
      expect(stream.heldCount).toBe(0)
      expect(stream.lines.map((l) => l.raw)).toEqual(['a', 'b', 'c'])
    })

    it('does not hold more lines than the maximum', () => {
      const { stream, es } = setup({ max: 3 })
      es.open()
      stream.pause()
      for (let n = 1; n <= 10; n++) es.message(`line ${n}`)
      expect(stream.heldCount).toBe(3)

      stream.resume()
      expect(stream.lines.map((l) => l.raw)).toEqual(['line 8', 'line 9', 'line 10'])
    })

    it('applies the maximum to lines already shown when it resumes', () => {
      const { stream, es } = setup({ max: 3 })
      es.open()
      es.message('1')
      es.message('2')
      stream.pause()
      es.message('3')
      es.message('4')
      stream.resume()
      expect(stream.lines.map((l) => l.raw)).toEqual(['2', '3', '4'])
    })
  })

  it('clears the shown and the held lines', () => {
    const { stream, es, onChange } = setup()
    es.open()
    es.message('a')
    stream.pause()
    es.message('b')
    onChange.mockClear()

    stream.clear()
    expect(stream.lines).toEqual([])
    expect(stream.heldCount).toBe(0)
    expect(onChange).toHaveBeenCalled()
  })

  it('closes the connection when stopped and ignores anything that arrives afterwards', () => {
    const { stream, es } = setup()
    es.open()
    stream.stop()
    expect(es.closed).toBe(true)
    expect(stream.status).toBe('closed')

    es.message('late')
    es.fail(FakeEventSource.CLOSED)
    expect(stream.lines).toEqual([])
    expect(stream.status).toBe('closed')
  })
})
