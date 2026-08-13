const tusState = vi.hoisted(() => ({
  previousError: null as Error | null,
  defaultShouldRetry: vi.fn<(...args: unknown[]) => boolean>(() => true),
  instances: [] as Array<{
    options: Record<string, unknown>
    abort: ReturnType<typeof vi.fn>
    start: ReturnType<typeof vi.fn>
  }>,
}))

vi.mock('tus-js-client', () => ({
  defaultOptions: {
    onShouldRetry: (...args: unknown[]) => tusState.defaultShouldRetry(...args),
    fingerprint: (file: File) => Promise.resolve(`default-${file.name}-${file.size}`),
  },
  Upload: class {
    options: Record<string, unknown>
    abort = vi.fn(() => Promise.resolve())
    start = vi.fn()

    constructor(_file: File, options: Record<string, unknown>) {
      this.options = options
      tusState.instances.push(this)
    }

    findPreviousUploads() { return tusState.previousError ? Promise.reject(tusState.previousError) : Promise.resolve([]) }
    resumeFromPreviousUpload() { /* No prior upload in this test. */ }
  },
}))

import { delay, http, HttpResponse } from 'msw'
import { api, uploadResumable } from '../api/client'
import { server } from './server'

describe('resumable upload transport', () => {
  afterEach(() => {
    vi.useRealTimers()
    tusState.previousError = null
    tusState.defaultShouldRetry.mockReset()
    tusState.defaultShouldRetry.mockReturnValue(true)
    tusState.instances.length = 0
  })

  it('sends cookies, declares non-overwrite intent, and aborts after 30 seconds without progress', async () => {
    vi.useFakeTimers()
    const onError = vi.fn()
    uploadResumable('/notes.txt', new File(['notes'], 'notes.txt', { type: 'text/plain' }), {
      onProgress: vi.fn(),
      onSuccess: vi.fn(),
      onError,
    })
    const instance = tusState.instances[0]!
    expect(instance.options.metadata).toEqual({ filename: 'notes.txt', path: '/notes.txt', filetype: 'text/plain', overwrite: 'false' })
    expect(instance.options.chunkSize).toBe(32 * 1024 * 1024)
    expect(instance.options.parallelUploads).toBe(1)

    const xhr = new XMLHttpRequest()
    const beforeRequest = instance.options.onBeforeRequest as (request: { getUnderlyingObject: () => unknown }) => void
    beforeRequest({ getUnderlyingObject: () => xhr })
    expect(xhr.withCredentials).toBe(true)

    await vi.advanceTimersByTimeAsync(30_001)
    expect(instance.abort).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: 'Upload stopped after 30 seconds without progress.' }))
  })

  it('isolates resumable state by destination and overwrite policy', async () => {
    const file = new File(['same'], 'book.epub', { type: 'application/epub+zip', lastModified: 123 })
    uploadResumable('/first/book.epub', file, { onProgress: vi.fn(), onSuccess: vi.fn(), onError: vi.fn() })
    uploadResumable('/second/book.epub', file, { onProgress: vi.fn(), onSuccess: vi.fn(), onError: vi.fn() })
    uploadResumable('/first/book.epub', file, { onProgress: vi.fn(), onSuccess: vi.fn(), onError: vi.fn() }, true)

    const fingerprints = await Promise.all(tusState.instances.map((instance) => {
      const fingerprint = instance.options.fingerprint as (file: File, options: Record<string, unknown>) => Promise<string>
      return fingerprint(file, instance.options)
    }))
    expect(new Set(fingerprints).size).toBe(3)
    expect(fingerprints[0]).toContain('/first/book.epub')
    expect(fingerprints[2]).toContain('true')
  })

  it('uses the default retry policy except for upload conflicts', () => {
    uploadResumable('/notes.txt', new File(['notes'], 'notes.txt'), {
      onProgress: vi.fn(),
      onSuccess: vi.fn(),
      onError: vi.fn(),
    })
    const instance = tusState.instances[0]!
    const shouldRetry = instance.options.onShouldRetry as (error: { originalResponse: { getStatus: () => number; getHeader: (name: string) => string | null | undefined } }, attempt: number, options: Record<string, unknown>) => boolean
    const conflict = { originalResponse: { getStatus: () => 409, getHeader: () => null } }
    expect(shouldRetry(conflict, 0, instance.options)).toBe(false)
    expect(tusState.defaultShouldRetry).not.toHaveBeenCalled()

    const offsetConflict = { originalResponse: { getStatus: () => 409, getHeader: (name: string) => name === 'Upload-Offset' ? '32' : undefined } }
    expect(shouldRetry(offsetConflict, 1, instance.options)).toBe(true)
    expect(tusState.defaultShouldRetry).toHaveBeenLastCalledWith(offsetConflict, 1, instance.options)

    const serverError = { originalResponse: { getStatus: () => 503, getHeader: () => undefined } }
    expect(shouldRetry(serverError, 2, instance.options)).toBe(true)
    expect(tusState.defaultShouldRetry).toHaveBeenLastCalledWith(serverError, 2, instance.options)
    tusState.defaultShouldRetry.mockReturnValue(false)
    expect(shouldRetry(serverError, 3, instance.options)).toBe(false)
    expect(tusState.defaultShouldRetry).toHaveBeenLastCalledWith(serverError, 3, instance.options)
  })

  it('disarms the progress watchdog after all bytes are sent while awaiting the response', async () => {
    vi.useFakeTimers()
    const onError = vi.fn()
    uploadResumable('/large.bin', new File(['large'], 'large.bin'), {
      onProgress: vi.fn(),
      onSuccess: vi.fn(),
      onError,
    })
    const instance = tusState.instances[0]!
    const onProgress = instance.options.onProgress as (uploaded: number, total: number) => void

    onProgress(5, 5)
    await vi.advanceTimersByTimeAsync(30_001)

    expect(instance.abort).not.toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()
  })

  it('settles once and clears the watchdog when resume discovery fails', async () => {
    vi.useFakeTimers()
    tusState.previousError = new Error('resume lookup failed')
    const onError = vi.fn()
    uploadResumable('/notes.txt', new File(['notes'], 'notes.txt'), {
      onProgress: vi.fn(),
      onSuccess: vi.fn(),
      onError,
    })

    await vi.advanceTimersByTimeAsync(0)
    expect(onError).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(tusState.previousError)
    await vi.advanceTimersByTimeAsync(30_001)
    expect(onError).toHaveBeenCalledOnce()
    expect(tusState.instances[0]!.abort).not.toHaveBeenCalled()
  })

  it('terminates a resumable upload when its signal is cancelled', async () => {
    const controller = new AbortController()
    const onError = vi.fn()
    uploadResumable('/large.bin', new File(['large'], 'large.bin'), {
      onProgress: vi.fn(),
      onSuccess: vi.fn(),
      onError,
    }, false, controller.signal)

    controller.abort()
    await Promise.resolve()

    expect(tusState.instances[0]!.abort).toHaveBeenCalledWith(true)
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ name: 'AbortError' }))
    expect(tusState.instances[0]!.start).not.toHaveBeenCalled()
  })

  it('aborts a direct upload when its signal is cancelled', async () => {
    server.use(http.put('*/api/v1/files/content', async () => {
      await delay(1_000)
      return new HttpResponse(null, { status: 204 })
    }))
    const controller = new AbortController()
    const upload = api.files.uploadWithProgress('/notes.txt', new File(['notes'], 'notes.txt'), false, vi.fn(), controller.signal)

    controller.abort()

    await expect(upload).rejects.toMatchObject({ name: 'AbortError' })
  })
})
