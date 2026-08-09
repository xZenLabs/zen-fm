import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

export const handlers = [
  http.get('http://localhost/api/v1/session', () => HttpResponse.json({ authenticated: true, setupRequired: false, csrfToken: 'a'.repeat(32) })),
  http.get('http://localhost/api/v1/settings', () => HttpResponse.json({ theme: 'system', locale: 'en', showHidden: false, clientTimeoutSeconds: 30, advancedMode: false, root: '/mnt/us', secureTransport: true })),
  http.get('http://localhost/api/v1/tokens', () => HttpResponse.json([])),
  http.get('http://localhost/api/v1/shares', () => HttpResponse.json([])),
  http.get('http://localhost/api/v1/files', () => HttpResponse.json({ path: '/', advancedMode: false, entries: [] })),
  http.get('http://localhost/api/v1/usage', () => HttpResponse.json({ used: 1024, total: 4096 })),
]

export const server = setupServer(...handlers)
