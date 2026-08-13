import * as tus from 'tus-js-client'
import type {
  Checksum,
  CopySizePlan,
  DiskUsage,
  CreatedToken,
  CreateShareInput,
  CreateTokenInput,
  FileListing,
  PersonalToken,
  PublicShare,
  SearchResult,
  Session,
  Settings,
  Share,
  ProblemDetails,
} from './types'

const API_ROOT = '/api/v1'
const DEFAULT_TIMEOUT_MS = 30_000
const CSRF_HEADER = 'X-ZenFM-CSRF'

let csrfToken = ''
let clientTimeoutMs = DEFAULT_TIMEOUT_MS
const unauthorizedListeners = new Set<() => void>()

export class ApiError extends Error {
  readonly status: number
  readonly problem?: ProblemDetails

  constructor(status: number, problem?: ProblemDetails) {
    super(problem?.detail ?? problem?.title ?? `Request failed (${status})`)
    this.name = 'ApiError'
    this.status = status
    this.problem = problem
  }
}

export function onUnauthorized(listener: () => void) {
  unauthorizedListeners.add(listener)
  return () => { unauthorizedListeners.delete(listener) }
}

export function setClientTimeout(seconds: number) {
  clientTimeoutMs = Number.isFinite(seconds) && seconds > 0 ? seconds * 1_000 : 0
}

function setCsrfFrom(value: unknown, response?: Response) {
  const headerToken = response?.headers.get(CSRF_HEADER)
  const bodyToken = value && typeof value === 'object' && 'csrfToken' in value
    ? (value as { csrfToken?: unknown }).csrfToken
    : undefined
  const nextToken = headerToken ?? (typeof bodyToken === 'string' ? bodyToken : undefined)
  if (nextToken) csrfToken = nextToken
}

function query(params: Record<string, string | number | boolean | undefined>) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined) search.set(key, String(value))
  }
  const encoded = search.toString()
  return encoded ? `?${encoded}` : ''
}

interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: BodyInit | Record<string, unknown>
  timeoutMs?: number
  skipUnauthorizedEvent?: boolean
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const controller = new AbortController()
  const timeoutMs = options.timeoutMs ?? clientTimeoutMs
  const timeout = timeoutMs > 0 ? window.setTimeout(() => controller.abort(), timeoutMs) : undefined
  const externalSignal = options.signal
  const abort = () => controller.abort(externalSignal?.reason)
  externalSignal?.addEventListener('abort', abort, { once: true })

  const method = options.method?.toUpperCase() ?? 'GET'
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) {
    headers.set(CSRF_HEADER, csrfToken)
  }

  let body = options.body
  if (body && typeof body === 'object' && !(body instanceof Blob) && !(body instanceof FormData) && !(body instanceof URLSearchParams) && !(body instanceof ArrayBuffer)) {
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(body)
  }

  try {
    const fetchOptions: RequestOptions = { ...options }
    delete fetchOptions.timeoutMs
    delete fetchOptions.skipUnauthorizedEvent
    delete fetchOptions.body
    const response = await fetch(path, {
      ...fetchOptions,
      method,
      headers,
      body: body as BodyInit | undefined,
      credentials: 'same-origin',
      signal: controller.signal,
    })

    if (!response.ok) {
      let problem: ProblemDetails | undefined
      if (response.headers.get('content-type')?.includes('json')) {
        problem = await response.json() as ProblemDetails
      }
      if (response.status === 401 && !options.skipUnauthorizedEvent) {
        csrfToken = ''
        unauthorizedListeners.forEach((listener) => listener())
      }
      throw new ApiError(response.status, problem)
    }

    if (response.status === 204) return undefined as T
    const contentType = response.headers.get('content-type') ?? ''
    if (!contentType.includes('json')) return await response.text() as T
    const value = await response.json() as T
    setCsrfFrom(value, response)
    return value
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ApiError(408, { title: 'Request timed out', status: 408 })
    }
    throw error
  } finally {
    window.clearTimeout(timeout)
    externalSignal?.removeEventListener('abort', abort)
  }
}

async function requestBlob(path: string): Promise<Blob> {
  const controller = new AbortController()
  let timeout = clientTimeoutMs > 0 ? window.setTimeout(() => controller.abort(), clientTimeoutMs) : undefined
  try {
    const response = await fetch(path, {
      headers: { Accept: 'application/pdf, application/epub+zip, image/*, text/html;q=0.9, */*;q=0.5' },
      credentials: 'same-origin',
      signal: controller.signal,
    })
    // The normal client timeout covers connection/response startup only. Once
    // headers arrive, the server's per-write progress deadline governs this
    // bounded stream without imposing a total transfer deadline.
    window.clearTimeout(timeout)
    timeout = undefined
    if (!response.ok) {
      let problem: ProblemDetails | undefined
      if (response.headers.get('content-type')?.includes('json')) problem = await response.json() as ProblemDetails
      if (response.status === 401) {
        csrfToken = ''
        unauthorizedListeners.forEach((listener) => listener())
      }
      throw new ApiError(response.status, problem)
    }
    return await response.blob()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ApiError(408, { title: 'Request timed out', status: 408 })
    }
    throw error
  } finally {
    window.clearTimeout(timeout)
  }
}

interface CopyProgressEvent {
  copiedBytes: number
  done?: boolean
  error?: ProblemDetails
}

async function copyWithProgress(source: string, destination: string, overwrite: boolean, onProgress: (copiedBytes: number) => void) {
  const controller = new AbortController()
  let timeout = clientTimeoutMs > 0 ? window.setTimeout(() => controller.abort(), clientTimeoutMs) : undefined
  const headers = new Headers({ Accept: 'application/x-ndjson', 'Content-Type': 'application/json' })
  if (csrfToken) headers.set(CSRF_HEADER, csrfToken)
  try {
    const response = await fetch(`${API_ROOT}/files/copy`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ source, destination, overwrite }),
      credentials: 'same-origin',
      signal: controller.signal,
    })
    window.clearTimeout(timeout)
    timeout = undefined
    if (!response.ok) {
      let problem: ProblemDetails | undefined
      if (response.headers.get('content-type')?.includes('json')) problem = await response.json() as ProblemDetails
      if (response.status === 401) {
        csrfToken = ''
        unauthorizedListeners.forEach((listener) => listener())
      }
      throw new ApiError(response.status, problem)
    }
    setCsrfFrom(undefined, response)
    if (response.status === 204) return
    if (!response.body) throw new ApiError(500, { title: 'Invalid copy progress response', status: 500 })

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffered = ''
    let done = false
    const consume = (line: string) => {
      if (!line.trim()) return
      const event = JSON.parse(line) as CopyProgressEvent
      if (event.error) throw new ApiError(event.error.status ?? 500, event.error)
      if (Number.isFinite(event.copiedBytes) && event.copiedBytes >= 0) onProgress(event.copiedBytes)
      if (event.done) done = true
    }
    for (;;) {
      const chunk = await reader.read()
      buffered += decoder.decode(chunk.value, { stream: !chunk.done })
      let newline = buffered.indexOf('\n')
      while (newline >= 0) {
        consume(buffered.slice(0, newline))
        buffered = buffered.slice(newline + 1)
        newline = buffered.indexOf('\n')
      }
      if (chunk.done) break
    }
    consume(buffered)
    if (!done) throw new ApiError(500, { title: 'Copy progress ended unexpectedly', status: 500 })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new ApiError(408, { title: 'Request timed out', status: 408 })
    }
    throw error
  } finally {
    window.clearTimeout(timeout)
  }
}

export const api = {
  session: {
    get: () => request<Session>(`${API_ROOT}/session`, { skipUnauthorizedEvent: true }),
    login: (username: string, password: string) => request<Session>(`${API_ROOT}/session`, {
      method: 'POST',
      body: { username, password },
      skipUnauthorizedEvent: true,
    }),
    logout: async () => {
      await request<void>(`${API_ROOT}/session`, { method: 'DELETE' })
      csrfToken = ''
    },
  },
  owner: {
    changePassword: (currentPassword: string | undefined, newPassword: string) => request<Session>(`${API_ROOT}/owner/password`, {
      method: 'PUT',
      body: { currentPassword, newPassword },
    }),
  },
  settings: {
    get: () => request<Settings>(`${API_ROOT}/settings`),
    update: (settings: Partial<Settings>) => request<Settings>(`${API_ROOT}/settings`, {
      method: 'PUT',
      body: settings,
    }),
  },
  files: {
    list: (path: string, hidden?: boolean) => request<FileListing>(`${API_ROOT}/files${query({ path, hidden })}`),
    createDirectory: (path: string) => request<void>(`${API_ROOT}/files/directory`, {
      method: 'POST',
      body: { path },
    }),
    createText: (path: string) => request<void>(`${API_ROOT}/files/content${query({ path })}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'text/plain; charset=utf-8', 'If-None-Match': '*' },
      body: '',
    }),
    saveText: (path: string, content: string) => request<void>(`${API_ROOT}/files/content${query({ path })}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
      body: content,
      timeoutMs: 0,
    }),
    upload: (path: string, file: File, overwrite = false) => request<void>(`${API_ROOT}/files/content${query({ path })}`, {
      method: 'PUT',
      headers: { 'Content-Type': file.type || 'application/octet-stream', ...(overwrite ? {} : { 'If-None-Match': '*' }) },
      body: file,
      timeoutMs: 0,
    }),
    remove: (path: string, recursive: boolean) => request<void>(`${API_ROOT}/files${query({ path, recursive })}`, {
      method: 'DELETE',
    }),
    move: (source: string, destination: string, overwrite = false) => request<void>(`${API_ROOT}/files/move`, {
      method: 'POST',
      body: { source, destination, overwrite },
    }),
    copy: (source: string, destination: string, overwrite = false) => request<void>(`${API_ROOT}/files/copy`, {
      method: 'POST',
      body: { source, destination, overwrite },
    }),
    copySize: (sources: string[]) => request<CopySizePlan>(`${API_ROOT}/files/copy-size`, {
      method: 'POST',
      body: { sources },
    }),
    copyWithProgress: (source: string, destination: string, overwrite: boolean, onProgress: (copiedBytes: number) => void) => copyWithProgress(source, destination, overwrite, onProgress),
    rawUrl: (path: string) => `${API_ROOT}/files/raw${query({ path })}`,
    previewUrl: (path: string, width = 1600, height = 1200) => `${API_ROOT}/files/preview${query({ path, width, height })}`,
    readText: (path: string) => request<string>(`${API_ROOT}/files/content${query({ path })}`, {
      headers: { Accept: 'text/plain' },
    }),
    readPreviewText: (path: string) => request<string>(`${API_ROOT}/files/preview${query({ path })}`, {
      headers: { Accept: 'text/plain' },
    }),
    readPreviewBlob: (path: string) => requestBlob(`${API_ROOT}/files/preview${query({ path })}`),
    checksum: (path: string) => request<Checksum>(`${API_ROOT}/files/checksum${query({ path })}`),
    createArchiveTicket: (paths: string[], format: 'zip' | 'tar' | 'tar.gz' = 'zip') => request<{ url: string }>(`${API_ROOT}/files/archive-tickets`, {
      method: 'POST', body: { paths, format },
    }),
  },
  usage: () => request<DiskUsage>(`${API_ROOT}/usage`),
  search: (path: string, term: string, hidden?: boolean, limit = 250) => request<SearchResult>(`${API_ROOT}/search${query({ path, q: term, hidden, limit })}`, {
    timeoutMs: 120_000,
  }),
  shares: {
    list: () => request<Share[]>(`${API_ROOT}/shares`),
    create: (input: CreateShareInput) => request<Share>(`${API_ROOT}/shares`, {
      method: 'POST',
      body: input as unknown as Record<string, unknown>,
    }),
    get: (id: string) => request<Share>(`${API_ROOT}/shares/${encodeURIComponent(id)}`),
    remove: (id: string) => request<void>(`${API_ROOT}/shares/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    public: (secret: string, password?: string, path = '/') => request<PublicShare>(`${API_ROOT}/public/shares/${encodeURIComponent(secret)}${query({ path })}`, password ? {
      method: 'POST',
      body: { password },
      skipUnauthorizedEvent: true,
    } : { skipUnauthorizedEvent: true }),
    publicRawUrl: (secret: string, path?: string) => `${API_ROOT}/public/shares/${encodeURIComponent(secret)}/raw${query({ path })}`,
  },
  tokens: {
    list: () => request<PersonalToken[]>(`${API_ROOT}/tokens`),
    create: (input: CreateTokenInput) => request<CreatedToken>(`${API_ROOT}/tokens`, {
      method: 'POST',
      body: input as unknown as Record<string, unknown>,
    }),
    remove: (id: string) => request<void>(`${API_ROOT}/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  },
}

export function uploadResumable(path: string, file: File, callbacks: {
  onProgress: (uploaded: number, total: number) => void
  onSuccess: () => void
  onError: (error: Error) => void
}, overwrite = false) {
  let stallTimer: number | undefined
  let settled = false
  const clearStallTimer = () => window.clearTimeout(stallTimer)
  const fail = (error: Error) => {
    if (settled) return
    settled = true
    clearStallTimer()
    callbacks.onError(error)
  }
  const armStallTimer = () => {
    clearStallTimer()
    stallTimer = window.setTimeout(() => {
      void upload.abort()
      fail(new Error('Upload stopped after 30 seconds without progress.'))
    }, 30_000)
  }
  const upload = new tus.Upload(file, {
    endpoint: `${API_ROOT}/uploads`,
    metadata: { filename: file.name, path, filetype: file.type, overwrite: String(overwrite) },
    headers: csrfToken ? { [CSRF_HEADER]: csrfToken } : {},
    retryDelays: [0, 1_000, 3_000, 5_000, 10_000],
    removeFingerprintOnSuccess: true,
    onBeforeRequest: (request) => {
      const transport = request.getUnderlyingObject() as unknown
      if (transport instanceof XMLHttpRequest) transport.withCredentials = true
    },
    onError: fail,
    onProgress: (uploaded, total) => {
      armStallTimer()
      callbacks.onProgress(uploaded, total)
    },
    onSuccess: () => {
      if (settled) return
      settled = true
      clearStallTimer()
      callbacks.onSuccess()
    },
  })
  armStallTimer()
  upload.findPreviousUploads().then((previous) => {
    const first = previous[0]
    if (first) upload.resumeFromPreviousUpload(first)
    upload.start()
  }).catch(fail)
  return upload
}

export function isConflictError(error: unknown) {
  if (error instanceof ApiError) return error.status === 409
  if (!error || typeof error !== 'object' || !('originalResponse' in error)) return false
  const response = (error as { originalResponse?: { getStatus?: () => number } }).originalResponse
  return response?.getStatus?.() === 409
}
