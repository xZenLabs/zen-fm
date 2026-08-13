const tusState = vi.hoisted(() => ({
  previousError: null as Error | null,
  instances: [] as Array<{
    options: Record<string, unknown>
    abort: ReturnType<typeof vi.fn>
    start: ReturnType<typeof vi.fn>
  }>,
}))

vi.mock('tus-js-client', () => ({
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

    const xhr = new XMLHttpRequest()
    const beforeRequest = instance.options.onBeforeRequest as (request: { getUnderlyingObject: () => unknown }) => void
    beforeRequest({ getUnderlyingObject: () => xhr })
    expect(xhr.withCredentials).toBe(true)

    await vi.advanceTimersByTimeAsync(30_001)
    expect(instance.abort).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: 'Upload stopped after 30 seconds without progress.' }))
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
