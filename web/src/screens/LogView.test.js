import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { parseLogLine } from '../lib/logline.js'
import LogView from './LogView.svelte'

let nextId = 0
const line = (raw) => ({ id: ++nextId, ...parseLogLine(raw) })

const accessLine = line(
  JSON.stringify({
    ts: 1700000000.5,
    status: 502,
    duration: 0.0123,
    request: { method: 'POST', host: 'api.example.com', uri: '/v1/items' },
  }),
)
const textLine = line('some plain log text')

function props(overrides = {}) {
  return {
    lines: [],
    status: 'live',
    reason: '',
    paused: false,
    heldCount: 0,
    filter: '',
    onpause: vi.fn(),
    onresume: vi.fn(),
    onclear: vi.fn(),
    onfilter: vi.fn(),
    ...overrides,
  }
}

describe('LogView status', () => {
  it.each([
    ['connecting', 'Connecting…'],
    ['live', 'Live'],
    ['reconnecting', 'Reconnecting…'],
    ['closed', 'Disconnected'],
  ])('shows %s as %s', (status, text) => {
    render(LogView, props({ status }))
    expect(screen.getByRole('status').textContent).toContain(text)
  })

  it('shows why the stream is unavailable', () => {
    render(LogView, props({ status: 'unavailable', reason: 'no access log found' }))
    const status = screen.getByRole('status')
    expect(status.textContent).toContain('Unavailable')
    expect(status.textContent).toContain('no access log found')
  })

  it('exposes the state for styling', () => {
    render(LogView, props({ status: 'reconnecting' }))
    expect(screen.getByRole('status').dataset.state).toBe('reconnecting')
  })
})

describe('LogView lines', () => {
  it('summarises a Caddy access log line', () => {
    render(LogView, props({ lines: [accessLine] }))
    const row = screen.getByTestId(`log-${accessLine.id}`)
    for (const part of ['502', 'POST', 'api.example.com', '/v1/items', '12 ms']) {
      expect(row.textContent).toContain(part)
    }
    expect(row.dataset.status).toBe('5xx')
  })

  it('shows a line that is not an access log entry as it is', () => {
    render(LogView, props({ lines: [textLine] }))
    expect(screen.getByTestId(`log-${textLine.id}`).textContent).toContain('some plain log text')
  })

  it('says there is nothing yet when live with no lines', () => {
    render(LogView, props({ lines: [], status: 'live' }))
    expect(screen.getByText('Waiting for requests…')).toBeTruthy()
  })

  it('renders hostile log text as text', () => {
    const hostile = line('<img src=x onerror=alert(1)> <script>alert(2)</script>')
    const { container } = render(LogView, props({ lines: [hostile] }))
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('script')).toBeNull()
    expect(screen.getByTestId(`log-${hostile.id}`).textContent).toContain('<img src=x onerror=alert(1)>')
  })

  it('renders hostile fields of an access log line as text', () => {
    const hostile = line(JSON.stringify({ status: 200, request: { method: 'GET', host: '<b>x</b>', uri: '<i>y</i>' } }))
    const { container } = render(LogView, props({ lines: [hostile] }))
    expect(container.querySelector('b, i')).toBeNull()
  })
})

describe('LogView filter', () => {
  it('shows only the lines that match the filter', () => {
    render(LogView, props({ lines: [accessLine, textLine], filter: 'plain' }))
    expect(screen.queryByTestId(`log-${accessLine.id}`)).toBeNull()
    expect(screen.getByTestId(`log-${textLine.id}`)).toBeTruthy()
  })

  it('says when nothing matches', () => {
    render(LogView, props({ lines: [accessLine], filter: 'zzz' }))
    expect(screen.getByText('No lines match the filter')).toBeTruthy()
  })

  it('reports what is typed in the filter box', async () => {
    const p = props()
    render(LogView, p)
    await fireEvent.input(screen.getByRole('searchbox', { name: 'Filter' }), { target: { value: 'api' } })
    expect(p.onfilter).toHaveBeenCalledWith('api')
  })

  it('shows the current filter in the box', () => {
    render(LogView, props({ filter: 'api' }))
    expect(screen.getByRole('searchbox', { name: 'Filter' }).value).toBe('api')
  })
})

describe('LogView controls', () => {
  it('pauses', async () => {
    const p = props()
    render(LogView, p)
    await fireEvent.click(screen.getByRole('button', { name: 'Pause' }))
    expect(p.onpause).toHaveBeenCalledOnce()
    expect(p.onresume).not.toHaveBeenCalled()
  })

  it('resumes, and says how many lines are waiting', async () => {
    const p = props({ paused: true, heldCount: 7 })
    render(LogView, p)
    const button = screen.getByRole('button', { name: /Resume/ })
    expect(button.textContent).toContain('7 new')
    await fireEvent.click(button)
    expect(p.onresume).toHaveBeenCalledOnce()
    expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull()
  })

  it('clears', async () => {
    const p = props({ lines: [textLine] })
    render(LogView, p)
    await fireEvent.click(screen.getByRole('button', { name: 'Clear' }))
    expect(p.onclear).toHaveBeenCalledOnce()
  })
})
