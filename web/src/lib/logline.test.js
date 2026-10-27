import { describe, expect, it } from 'vitest'
import { filterLines, formatDuration, parseLogLine, statusClass } from './logline.js'

const caddyLine = JSON.stringify({
  level: 'info',
  ts: 1700000000.5,
  logger: 'http.log.access.log0',
  msg: 'handled request',
  request: { remote_ip: '10.0.0.7', method: 'GET', host: 'app.example.com', uri: '/items?id=1' },
  duration: 0.0123,
  size: 512,
  status: 200,
})

const rawOnly = (raw) => ({ raw, time: null, status: null, method: null, host: null, uri: null, duration: null })

describe('parseLogLine', () => {
  it('reads the fields of a Caddy access log line', () => {
    const line = parseLogLine(caddyLine)
    expect(line.raw).toBe(caddyLine)
    expect(line.time).toEqual(new Date(1700000000500))
    expect(line).toMatchObject({
      status: 200,
      method: 'GET',
      host: 'app.example.com',
      uri: '/items?id=1',
      duration: 0.0123,
    })
  })

  it.each([
    ['plain text', 'just some text'],
    ['an empty string', ''],
    ['JSON that is not an object', '[1,2]'],
    ['a JSON string', '"hello"'],
    ['null', 'null'],
    ['truncated JSON', '{"status":200,"requ [truncated]'],
  ])('keeps only the raw text for %s', (_name, raw) => {
    expect(parseLogLine(raw)).toEqual(rawOnly(raw))
  })

  it('ignores fields of the wrong type', () => {
    const raw = JSON.stringify({ ts: 'soon', status: '200', duration: 'long', request: { method: 5, host: null, uri: {} } })
    expect(parseLogLine(raw)).toEqual(rawOnly(raw))
  })

  it('tolerates a request that is not an object', () => {
    expect(parseLogLine(JSON.stringify({ status: 204, request: 'GET /' })).status).toBe(204)
  })
})

describe('statusClass', () => {
  it.each([
    [200, '2xx'],
    [204, '2xx'],
    [301, '3xx'],
    [404, '4xx'],
    [499, '4xx'],
    [500, '5xx'],
    [599, '5xx'],
    [100, 'other'],
    [600, 'other'],
    [null, 'other'],
  ])('%s is %s', (status, want) => {
    expect(statusClass(status)).toBe(want)
  })
})

describe('formatDuration', () => {
  it.each([
    [0.0123, '12 ms'],
    [0.0004, '<1 ms'],
    [0, '<1 ms'],
    [0.9994, '999 ms'],
    [1, '1.00 s'],
    [2.345, '2.35 s'],
    [null, ''],
  ])('%s is %j', (seconds, want) => {
    expect(formatDuration(seconds)).toBe(want)
  })
})

describe('filterLines', () => {
  const lines = [{ raw: 'GET /Api/Users' }, { raw: 'POST /login' }, { raw: 'get /health' }]

  it('returns every line for an empty filter', () => {
    expect(filterLines(lines, '')).toEqual(lines)
  })

  it('matches ignoring case', () => {
    expect(filterLines(lines, 'GET')).toEqual([lines[0], lines[2]])
  })

  it('treats the text literally, not as a pattern', () => {
    expect(filterLines(lines, '.*')).toEqual([])
    expect(filterLines([{ raw: 'a.b' }, { raw: 'axb' }], 'a.b')).toEqual([{ raw: 'a.b' }])
  })
})
