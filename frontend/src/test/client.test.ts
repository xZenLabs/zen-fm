import { http, HttpResponse } from 'msw'
import { api, onUnauthorized } from '../api/client'
import { server } from './server'

describe('API client security', () => {
  it('keeps the session CSRF value in memory and sends it on mutations', async () => {
    let received = ''
    server.use(
      http.post('http://localhost/api/v1/session', () => HttpResponse.json({ authenticated: true, setupRequired: false, csrfToken: 'secure-session-csrf-value-1234567890' })),
      http.put('http://localhost/api/v1/settings', ({ request }) => {
        received = request.headers.get('X-ZenFM-CSRF') ?? ''
        return HttpResponse.json({ theme: 'dark', locale: 'en', showHidden: false, clientTimeoutSeconds: 30, advancedMode: false, root: '/', secureTransport: true })
      }),
    )

    await api.session.login('koreader', 'correct horse battery staple')
    await api.settings.update({ theme: 'dark' })

    expect(received).toBe('secure-session-csrf-value-1234567890')
    expect(localStorage).toHaveLength(0)
  })

  it('notifies the auth boundary when an authenticated request returns 401', async () => {
    const unauthorized = vi.fn()
    const unsubscribe = onUnauthorized(unauthorized)
    server.use(http.get('http://localhost/api/v1/settings', () => HttpResponse.json({ title: 'Unauthorized', status: 401 }, { status: 401, headers: { 'Content-Type': 'application/problem+json' } })))

    await expect(api.settings.get()).rejects.toEqual(expect.objectContaining({ status: 401 }))
    expect(unauthorized).toHaveBeenCalledOnce()
    unsubscribe()
  })
})

describe('API client file content', () => {
  const jsonSource = '{\n  "enabled": true\n}\n'

  it.each([
    ['preview', '/api/v1/files/preview', () => api.files.readPreviewText('/config.json')],
    ['editor', '/api/v1/files/content', () => api.files.readText('/config.json')],
  ])('keeps JSON source as text for the %s', async (_name, endpoint, read) => {
    let accept = ''
    server.use(http.get(`http://localhost${endpoint}`, ({ request }) => {
      accept = request.headers.get('Accept') ?? ''
      return new HttpResponse(jsonSource, { headers: { 'Content-Type': 'application/json' } })
    }))

    await expect(read()).resolves.toBe(jsonSource)
    expect(accept).toBe('text/plain')
  })
})
