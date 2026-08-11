import '@testing-library/jest-dom/vitest'
import { configure } from '@testing-library/react'
import { afterAll, afterEach, beforeAll, vi } from 'vitest'
import { server } from './server'
import '../i18n'

configure({ asyncUtilTimeout: 5_000 })

let interceptedFetch: typeof fetch

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

Object.defineProperty(navigator, 'clipboard', {
  configurable: true,
  value: { writeText: vi.fn().mockResolvedValue(undefined) },
})

Object.defineProperties(URL, {
  createObjectURL: { configurable: true, value: vi.fn(() => 'blob:test-preview') },
  revokeObjectURL: { configurable: true, value: vi.fn() },
})

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = ResizeObserverStub

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' })
  interceptedFetch = globalThis.fetch
  globalThis.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const resolved = typeof input === 'string' && input.startsWith('/') ? new URL(input, 'http://localhost') : input
    if (!init?.signal) return interceptedFetch(resolved, init)
    const compatibleInit = { ...init }
    delete compatibleInit.signal
    return interceptedFetch(resolved, compatibleInit)
  }
})
afterEach(() => {
  server.resetHandlers()
  localStorage.clear()
})
afterAll(() => {
  globalThis.fetch = interceptedFetch
  server.close()
})
