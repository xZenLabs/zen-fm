import { Readable } from 'node:stream'
import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Connect } from 'vite'
import { expect, it } from 'vitest'
import { createMockApiMiddleware } from './mockApi'

interface MockResponse {
  status: number
  headers: Record<string, string>
  body: string
}

function invoke(middleware: Connect.NextHandleFunction, method: string, url: string, body?: unknown) {
  return new Promise<MockResponse>((resolve, reject) => {
    const source = body === undefined ? [] : [JSON.stringify(body)]
    const request = Readable.from(source) as IncomingMessage
    request.method = method
    request.url = url
    request.headers = {
      cookie: 'zenfm_mock_session=demo',
      'content-type': 'application/json',
      'x-zenfm-csrf': 'zenfm-mock-csrf-token-0000000000',
    }
    let status = 0
    let headers: Record<string, string> = {}
    const response = {
      writeHead(nextStatus: number, nextHeaders: Record<string, string>) {
        status = nextStatus
        headers = nextHeaders
        return response
      },
      end(chunk?: string | Buffer) {
        resolve({ status, headers, body: chunk?.toString() ?? '' })
        return response
      },
    } as unknown as ServerResponse
    middleware(request, response, () => reject(new Error(`Middleware skipped ${method} ${url}`)))
  })
}

it('supports the copy-size and copy requests used by Paste in mock mode', async () => {
  const middleware = createMockApiMiddleware()

  const plan = await invoke(middleware, 'POST', '/api/v1/files/copy-size', { sources: ['/README.md'] })
  expect(plan.status).toBe(200)
  const planBody = JSON.parse(plan.body) as { items: Array<{ source: string; bytes: number }>; totalBytes: number }
  expect(planBody.items).toHaveLength(1)
  expect(planBody.items[0]?.source).toBe('/README.md')
  expect(planBody.items[0]?.bytes).toBeGreaterThan(0)
  expect(planBody.totalBytes).toBeGreaterThan(0)

  const copy = await invoke(middleware, 'POST', '/api/v1/files/copy', { source: '/README.md', destination: '/Documents/README.md', overwrite: false })
  expect(copy.status).toBe(204)

  const listing = await invoke(middleware, 'GET', '/api/v1/files?path=/Documents')
  expect(listing.status).toBe(200)
  const listingBody = JSON.parse(listing.body) as { entries: Array<{ path: string; type: string }> }
  expect(listingBody.entries).toContainEqual(expect.objectContaining({ path: '/Documents/README.md', type: 'file' }))
})
