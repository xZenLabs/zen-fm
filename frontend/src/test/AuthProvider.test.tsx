import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { useAuth, AuthProvider } from '../auth/AuthProvider'
import { server } from './server'

function Probe() {
  const { status, refresh, validationError } = useAuth()
  return <><span>{status}</span><span>{validationError ? 'disconnected' : 'connected'}</span><button onClick={() => void refresh().catch(() => undefined)}>Validate</button></>
}

describe('session deadline', () => {
  afterEach(() => vi.useRealTimers())

  it('validates at idle expiry and reschedules when the server reports recent activity', async () => {
    const start = new Date('2026-01-01T00:00:00Z')
    let validations = 0
    server.use(http.get('http://localhost/api/v1/session', () => {
      validations += 1
      if (validations === 3) return HttpResponse.json({ title: 'Unauthorized', status: 401 }, { status: 401 })
      const idleMilliseconds = validations === 1 ? 1_000 : 4_000
      return HttpResponse.json({
        authenticated: true,
        setupRequired: false,
        csrfToken: 'deadline-csrf-token-value-123456789',
        idleExpiresAt: new Date(start.valueOf() + idleMilliseconds).toISOString(),
        absoluteExpiresAt: new Date(start.valueOf() + 10_000).toISOString(),
      })
    }))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(start)
    render(<AuthProvider><Probe /></AuthProvider>)

    expect(await screen.findByText('authenticated')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(1_001))
    await waitFor(() => expect(validations).toBe(2))
    expect(screen.getByText('authenticated')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(3_000))
    await waitFor(() => expect(screen.getByText('anonymous')).toBeInTheDocument())
  })

  it('expires at the absolute deadline when it comes first', async () => {
    const start = new Date('2026-01-01T00:00:00Z')
    server.use(http.get('http://localhost/api/v1/session', () => HttpResponse.json({
      authenticated: true,
      setupRequired: false,
      csrfToken: 'absolute-csrf-token-value-1234567',
      idleExpiresAt: new Date(start.valueOf() + 10_000).toISOString(),
      absoluteExpiresAt: new Date(start.valueOf() + 750).toISOString(),
    })))
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(start)
    render(<AuthProvider><Probe /></AuthProvider>)

    expect(await screen.findByText('authenticated')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(751))
    await waitFor(() => expect(screen.getByText('anonymous')).toBeInTheDocument())
  })

  it('retains an authenticated cookie session through a transient validation failure', async () => {
    let validations = 0
    server.use(http.get('http://localhost/api/v1/session', () => {
      validations += 1
      if (validations === 2) return HttpResponse.error()
      return HttpResponse.json({
        authenticated: true,
        setupRequired: false,
        csrfToken: 'network-recovery-csrf-token-123456',
      })
    }))
    render(<AuthProvider><Probe /></AuthProvider>)

    expect(await screen.findByText('authenticated')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Validate' }))
    expect(await screen.findByText('disconnected')).toBeInTheDocument()
    expect(screen.getByText('authenticated')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Validate' }))
    await waitFor(() => expect(screen.getByText('connected')).toBeInTheDocument())
    expect(screen.getByText('authenticated')).toBeInTheDocument()
  })
})
