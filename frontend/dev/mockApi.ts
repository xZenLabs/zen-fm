import { createHash, randomBytes } from 'node:crypto'
import { posix } from 'node:path'
import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Connect, Plugin } from 'vite'

const USERNAME = 'koreader'
const DEMO_PASSWORD = 'koreader'
const SESSION_COOKIE = 'zenfm_mock_session=demo'
const CSRF_TOKEN = 'zenfm-mock-csrf-token-0000000000'
const MAX_BODY_BYTES = 16 * 1024 * 1024

type FileKind = 'file' | 'directory'

interface MockFile {
  type: FileKind
  content: Buffer
  mimeType?: string
  modifiedAt: string
}

interface MockShare {
  id: string
  secret: string
  path: string
  name?: string
  password?: string
  expiresAt?: string
  createdAt: string
}

interface MockToken {
  id: string
  name: string
  createdAt: string
  expiresAt: string
}

interface MockSettings {
  theme: 'light' | 'dark' | 'system'
  locale: string
  showHidden: boolean
  clientTimeoutSeconds: number
  advancedMode: boolean
  root: string
  secureTransport: boolean
}

function now() {
  return new Date().toISOString()
}

function file(content: string | Buffer, mimeType = 'text/plain; charset=utf-8'): MockFile {
  return { type: 'file', content: Buffer.isBuffer(content) ? content : Buffer.from(content), mimeType, modifiedAt: now() }
}

function directory(): MockFile {
  return { type: 'directory', content: Buffer.alloc(0), modifiedAt: now() }
}

function seedFiles() {
  const tinyPng = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
  return new Map<string, MockFile>([
    ['/', directory()],
    ['/Books', directory()],
    ['/Documents', directory()],
    ['/Pictures', directory()],
    ['/.zenfm', directory()],
    ['/README.md', file('# ZenFM mock library\n\nThis frontend is running without the Go server. Changes live in memory until Vite stops.\n', 'text/markdown; charset=utf-8')],
    ['/Books/Quiet Reading.txt', file('A small demo book list for testing search, previews, editing, sharing, and file actions.\n')],
    ['/Books/reading-list.csv', file('title,status\nThe Art of Stillness,reading\nZen Mind Beginner’s Mind,next\n', 'text/csv; charset=utf-8')],
    ['/Documents/notes.json', file('{\n  "device": "KOReader",\n  "mode": "mock"\n}\n', 'application/json; charset=utf-8')],
    ['/Pictures/zen.png', file(tinyPng, 'image/png')],
    ['/.zenfm/demo-state.txt', file('Hidden demo state\n')],
  ])
}

function normalizePath(value: string | null | undefined) {
  const input = value || '/'
  if (input.includes('\0') || input.split('/').includes('..')) throw new Error('Invalid path.')
  return posix.normalize(`/${input}`).replace(/\/$/, '') || '/'
}

function parentPath(path: string) {
  return path === '/' ? '/' : posix.dirname(path)
}

function mimeFor(path: string) {
  const extension = posix.extname(path).toLowerCase()
  return ({
    '.csv': 'text/csv; charset=utf-8',
    '.html': 'text/html; charset=utf-8',
    '.json': 'application/json; charset=utf-8',
    '.md': 'text/markdown; charset=utf-8',
    '.png': 'image/png',
    '.txt': 'text/plain; charset=utf-8',
  } as Record<string, string>)[extension] ?? 'application/octet-stream'
}

function entry(path: string, value: MockFile) {
  return {
    name: posix.basename(path),
    path,
    type: value.type,
    size: value.type === 'file' ? value.content.byteLength : 0,
    modifiedAt: value.modifiedAt,
    ...(value.mimeType ? { mimeType: value.mimeType } : {}),
    hidden: posix.basename(path).startsWith('.'),
    writable: true,
  }
}

function sendJSON(response: ServerResponse, status: number, value: unknown, headers: Record<string, string> = {}) {
  response.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', ...headers })
  response.end(JSON.stringify(value))
}

function sendProblem(response: ServerResponse, status: number, detail: string) {
  response.writeHead(status, { 'Content-Type': 'application/problem+json; charset=utf-8', 'Cache-Control': 'no-store' })
  response.end(JSON.stringify({ title: detail, detail, status }))
}

function sendEmpty(response: ServerResponse, status = 204, headers: Record<string, string> = {}) {
  response.writeHead(status, { 'Cache-Control': 'no-store', ...headers })
  response.end()
}

async function readBody(request: IncomingMessage) {
  const chunks: Buffer[] = []
  let size = 0
  for await (const chunk of request) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk as Uint8Array)
    size += bytes.byteLength
    if (size > MAX_BODY_BYTES) throw new Error('Mock request body is limited to 16 MiB.')
    chunks.push(bytes)
  }
  return Buffer.concat(chunks)
}

async function readJSON<T>(request: IncomingMessage) {
  const body = await readBody(request)
  try {
    return JSON.parse(body.toString('utf8') || '{}') as T
  } catch {
    throw new Error('Invalid JSON body.')
  }
}

function authenticated(request: IncomingMessage) {
  return request.headers.cookie?.split(';').some((part) => part.trim() === SESSION_COOKIE) ?? false
}

function session() {
  const time = Date.now()
  return {
    authenticated: true,
    username: USERNAME,
    setupRequired: false,
    csrfToken: CSRF_TOKEN,
    idleExpiresAt: new Date(time + 2 * 60 * 60 * 1000).toISOString(),
    absoluteExpiresAt: new Date(time + 12 * 60 * 60 * 1000).toISOString(),
  }
}

function copyTree(files: Map<string, MockFile>, source: string, destination: string) {
  const sourceNode = files.get(source)
  if (!sourceNode) throw new Error('Source does not exist.')
  if (files.has(destination)) throw new Error('Destination already exists.')
  if (!files.has(parentPath(destination))) throw new Error('Destination parent does not exist.')
  const sourcePrefix = `${source}/`
  for (const [path, value] of [...files]) {
    if (path !== source && !path.startsWith(sourcePrefix)) continue
    const target = path === source ? destination : `${destination}${path.slice(source.length)}`
    files.set(target, { ...value, content: Buffer.from(value.content), modifiedAt: now() })
  }
}

function copyTreeSize(files: Map<string, MockFile>, source: string) {
  if (!files.has(source)) throw new Error('Source does not exist.')
  const sourcePrefix = `${source}/`
  return [...files]
    .filter(([path]) => path === source || path.startsWith(sourcePrefix))
    .reduce((total, [, value]) => total + (value.type === 'file' ? value.content.byteLength : 0), 0)
}

function deleteTree(files: Map<string, MockFile>, path: string) {
  for (const candidate of [...files.keys()]) {
    if (candidate === path || candidate.startsWith(`${path}/`)) files.delete(candidate)
  }
}

function shareView(files: Map<string, MockFile>, share: MockShare, relativePath: string) {
  const root = normalizePath(share.path)
  const relative = normalizePath(relativePath)
  const target = root === '/' ? relative : normalizePath(`${root}${relative === '/' ? '' : relative}`)
  if (target !== root && !target.startsWith(`${root}/`)) throw new Error('Path is outside the share.')
  const node = files.get(target)
  if (!node) throw new Error('Shared path does not exist.')
  const base = { name: share.name || posix.basename(root), path: relative, ...(share.expiresAt ? { expiresAt: share.expiresAt } : {}) }
  if (node.type === 'file') return { ...base, entry: { ...entry(target, node), path: relative } }
  const children = [...files]
    .filter(([path]) => path !== target && parentPath(path) === target)
    .map(([path, value]) => ({ ...entry(path, value), path: root === '/' ? path : normalizePath(path.slice(root.length)) }))
    .sort((left, right) => left.name.localeCompare(right.name))
  return { ...base, entries: children }
}

export function createMockApiMiddleware(): Connect.NextHandleFunction {
  const files = seedFiles()
  let ownerPassword = DEMO_PASSWORD
  const createdAt = now()
  const shares: MockShare[] = [{ id: 'share-1', secret: 'demo-books', path: '/Books', name: 'Demo reading list', createdAt }]
  const tokens: MockToken[] = [{ id: 'token-1', name: 'Demo phone', createdAt, expiresAt: new Date(Date.now() + 30 * 86_400_000).toISOString() }]
  const unlockedShares = new Set<string>()
  let settings: MockSettings = {
    theme: 'system', locale: 'en', showHidden: false, clientTimeoutSeconds: 30,
    advancedMode: false, root: '/mock-storage', secureTransport: false,
  }
  let nextID = 2

  const handle = async (request: IncomingMessage, response: ServerResponse, next: () => void) => {
    const url = new URL(request.url || '/', 'http://zenfm.local')
    const path = url.pathname
    const method = request.method || 'GET'
    if (!path.startsWith('/api/v1/')) return next()

    try {
      if (path === '/api/v1/session' && method === 'POST') {
        const credentials = await readJSON<{ username?: string; password?: string }>(request)
        if (credentials.username !== USERNAME || credentials.password !== ownerPassword) return sendProblem(response, 401, 'Invalid username or password.')
        return sendJSON(response, 200, session(), { 'Set-Cookie': `${SESSION_COOKIE}; Path=/; HttpOnly; SameSite=Strict` })
      }

      const publicMatch = path.match(/^\/api\/v1\/public\/shares\/([^/]+)(\/raw)?$/)
      if (publicMatch) {
        const secret = decodeURIComponent(publicMatch[1] || '')
        const share = shares.find((candidate) => candidate.secret === secret)
        if (!share) return sendProblem(response, 404, 'Share not found.')
        const relative = normalizePath(url.searchParams.get('path'))
        if (publicMatch[2] === '/raw' && method === 'GET') {
          if (share.password && !unlockedShares.has(secret)) return sendProblem(response, 401, 'Share password required.')
          const root = normalizePath(share.path)
          const target = root === '/' ? relative : normalizePath(`${root}${relative === '/' ? '' : relative}`)
          const value = files.get(target)
          if (!value || value.type !== 'file') return sendProblem(response, 404, 'Shared file not found.')
          response.writeHead(200, { 'Content-Type': value.mimeType || mimeFor(target), 'Content-Length': value.content.byteLength, 'Cache-Control': 'no-store' })
          return response.end(value.content)
        }
        if (!['GET', 'POST'].includes(method)) return sendProblem(response, 405, 'Method not allowed.')
        if (share.password && !unlockedShares.has(secret)) {
          if (method === 'GET') return sendJSON(response, 200, { name: share.name || 'Shared files', path: relative, passwordRequired: true })
          const unlock = await readJSON<{ password?: string }>(request)
          if (unlock.password !== share.password) return sendProblem(response, 401, 'Invalid share password.')
          unlockedShares.add(secret)
        }
        return sendJSON(response, 200, shareView(files, share, relative))
      }

      if (!authenticated(request)) return sendProblem(response, 401, 'Authentication required.')
      if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && request.headers['x-zenfm-csrf'] !== CSRF_TOKEN) {
        return sendProblem(response, 403, 'CSRF token is missing or invalid.')
      }

      if (path === '/api/v1/session') {
        if (method === 'GET') return sendJSON(response, 200, session())
        if (method === 'DELETE') return sendEmpty(response, 204, { 'Set-Cookie': 'zenfm_mock_session=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0' })
      }

      if (path === '/api/v1/owner/password' && method === 'PUT') {
        const input = await readJSON<{ currentPassword?: string; newPassword?: string }>(request)
        if (input.currentPassword === undefined) return sendProblem(response, 401, 'Current password is required.')
        if (input.currentPassword !== ownerPassword) return sendProblem(response, 401, 'Current password is incorrect.')
        if (Array.from(input.newPassword ?? '').length < 12) return sendProblem(response, 422, 'New password must contain at least 12 characters.')
        ownerPassword = input.newPassword || ownerPassword
        return sendJSON(response, 200, session())
      }

      if (path === '/api/v1/settings') {
        if (method === 'GET') return sendJSON(response, 200, settings)
        if (method === 'PUT') {
          settings = { ...settings, ...await readJSON<Partial<MockSettings>>(request), advancedMode: false, root: '/mock-storage' }
          return sendJSON(response, 200, settings)
        }
      }

      if (path === '/api/v1/usage' && method === 'GET') {
        const used = [...files.values()].reduce((total, value) => total + value.content.byteLength, 7 * 1024 * 1024 * 1024)
        return sendJSON(response, 200, { used, total: 32 * 1024 * 1024 * 1024 })
      }

      if (path === '/api/v1/files' && method === 'GET') {
        const target = normalizePath(url.searchParams.get('path'))
        const value = files.get(target)
        if (!value || value.type !== 'directory') return sendProblem(response, 404, 'Directory not found.')
        const showHidden = url.searchParams.get('hidden') === 'true'
        const entries = [...files]
          .filter(([candidate]) => candidate !== target && parentPath(candidate) === target)
          .filter(([candidate]) => showHidden || !posix.basename(candidate).startsWith('.'))
          .map(([candidate, node]) => entry(candidate, node))
        return sendJSON(response, 200, { path: target, entries, advancedMode: false, disk: { used: 7 * 1024 ** 3, total: 32 * 1024 ** 3 } })
      }

      if (path === '/api/v1/files' && method === 'DELETE') {
        const target = normalizePath(url.searchParams.get('path'))
        if (target === '/') return sendProblem(response, 409, 'The mock root cannot be deleted.')
        const value = files.get(target)
        if (!value) return sendProblem(response, 404, 'Path not found.')
        const hasChildren = [...files.keys()].some((candidate) => candidate.startsWith(`${target}/`))
        if (hasChildren && url.searchParams.get('recursive') !== 'true') return sendProblem(response, 409, 'Directory is not empty.')
        deleteTree(files, target)
        return sendEmpty(response)
      }

      if (path === '/api/v1/files/directory' && method === 'POST') {
        const input = await readJSON<{ path?: string }>(request)
        const target = normalizePath(input.path)
        if (files.has(target)) return sendProblem(response, 409, 'Path already exists.')
        if (files.get(parentPath(target))?.type !== 'directory') return sendProblem(response, 404, 'Parent directory not found.')
        files.set(target, directory())
        return sendEmpty(response, 201)
      }

      if (path === '/api/v1/files/content') {
        const target = normalizePath(url.searchParams.get('path'))
        if (method === 'GET') {
          const value = files.get(target)
          if (!value || value.type !== 'file') return sendProblem(response, 404, 'File not found.')
          response.writeHead(200, { 'Content-Type': value.mimeType || mimeFor(target), 'Cache-Control': 'no-store' })
          return response.end(value.content)
        }
        if (method === 'PUT') {
          const existed = files.has(target)
          if (request.headers['if-none-match'] === '*' && files.has(target)) return sendProblem(response, 409, 'Path already exists.')
          if (files.get(parentPath(target))?.type !== 'directory') return sendProblem(response, 404, 'Parent directory not found.')
          const content = await readBody(request)
          files.set(target, file(content, request.headers['content-type'] || mimeFor(target)))
          return sendEmpty(response, existed ? 204 : 201)
        }
      }

      if (path === '/api/v1/files/copy-size' && method === 'POST') {
        const input = await readJSON<{ sources?: string[] }>(request)
        const sources = input.sources ?? []
        const items = sources.map((source) => {
          const normalized = normalizePath(source)
          return { source: normalized, bytes: copyTreeSize(files, normalized) }
        })
        return sendJSON(response, 200, { items, totalBytes: items.reduce((total, item) => total + item.bytes, 0) })
      }

      if ((path === '/api/v1/files/copy' || path === '/api/v1/files/move') && method === 'POST') {
        const input = await readJSON<{ source?: string; destination?: string }>(request)
        const source = normalizePath(input.source)
        const destination = normalizePath(input.destination)
        if (source === '/' || destination === '/') return sendProblem(response, 409, 'The mock root cannot be moved or replaced.')
        copyTree(files, source, destination)
        if (path.endsWith('/move')) deleteTree(files, source)
        return sendEmpty(response)
      }

      if ((path === '/api/v1/files/raw' || path === '/api/v1/files/preview') && method === 'GET') {
        const target = normalizePath(url.searchParams.get('path'))
        const value = files.get(target)
        if (!value || value.type !== 'file') return sendProblem(response, 404, 'File not found.')
        response.writeHead(200, { 'Content-Type': value.mimeType || mimeFor(target), 'Content-Length': value.content.byteLength, 'Cache-Control': 'no-store' })
        return response.end(value.content)
      }

      if (path === '/api/v1/files/checksum' && method === 'GET') {
        const target = normalizePath(url.searchParams.get('path'))
        const value = files.get(target)
        if (!value || value.type !== 'file') return sendProblem(response, 404, 'File not found.')
        return sendJSON(response, 200, { algorithm: 'sha256', value: createHash('sha256').update(value.content).digest('hex') })
      }

      if (path === '/api/v1/search' && method === 'GET') {
        const root = normalizePath(url.searchParams.get('path'))
        const term = (url.searchParams.get('q') || '').toLocaleLowerCase()
        const showHidden = url.searchParams.get('hidden') === 'true'
        const limit = Math.max(1, Math.min(Number(url.searchParams.get('limit')) || 250, 250))
        const matches = [...files]
          .filter(([candidate]) => candidate !== root && (root === '/' || candidate.startsWith(`${root}/`)))
          .filter(([candidate]) => showHidden || !candidate.split('/').some((part) => part.startsWith('.')))
          .filter(([candidate]) => posix.basename(candidate).toLocaleLowerCase().includes(term))
          .map(([candidate, value]) => entry(candidate, value))
        return sendJSON(response, 200, { entries: matches.slice(0, limit), truncated: matches.length > limit })
      }

      if (path === '/api/v1/files/archive-tickets' && method === 'POST') {
        return sendProblem(response, 501, 'Archive downloads are unavailable in frontend mock mode.')
      }
      if (path === '/api/v1/uploads' && ['OPTIONS', 'POST'].includes(method)) {
        return sendProblem(response, 501, 'Resumable uploads are unavailable in frontend mock mode; direct uploads under 8 MiB are supported.')
      }

      if (path === '/api/v1/shares') {
        if (method === 'GET') return sendJSON(response, 200, shares.map(({ secret, password, ...share }) => ({ ...share, url: `/s/${secret}`, passwordProtected: Boolean(password) })))
        if (method === 'POST') {
          const input = await readJSON<{ path?: string; name?: string; password?: string; expiresInSeconds?: number }>(request)
          const target = normalizePath(input.path)
          if (!files.has(target)) return sendProblem(response, 404, 'Path not found.')
          const id = `share-${nextID++}`
          const secret = randomBytes(12).toString('base64url')
          const share: MockShare = { id, secret, path: target, name: input.name, password: input.password, createdAt: now(), ...(input.expiresInSeconds ? { expiresAt: new Date(Date.now() + input.expiresInSeconds * 1000).toISOString() } : {}) }
          shares.push(share)
          return sendJSON(response, 201, { id, path: target, name: share.name, createdAt: share.createdAt, expiresAt: share.expiresAt, passwordProtected: Boolean(share.password), url: `/s/${secret}` })
        }
      }

      const shareMatch = path.match(/^\/api\/v1\/shares\/([^/]+)$/)
      if (shareMatch) {
        const index = shares.findIndex((candidate) => candidate.id === decodeURIComponent(shareMatch[1] || ''))
        if (index < 0) return sendProblem(response, 404, 'Share not found.')
        if (method === 'DELETE') { shares.splice(index, 1); return sendEmpty(response) }
        if (method === 'GET') {
          const share = shares[index]
          if (!share) return sendProblem(response, 404, 'Share not found.')
          return sendJSON(response, 200, { id: share.id, path: share.path, name: share.name, createdAt: share.createdAt, expiresAt: share.expiresAt, passwordProtected: Boolean(share.password) })
        }
      }

      if (path === '/api/v1/tokens') {
        if (method === 'GET') return sendJSON(response, 200, tokens)
        if (method === 'POST') {
          const input = await readJSON<{ name?: string; expiresInSeconds?: number }>(request)
          if (!input.name) return sendProblem(response, 422, 'Token name is required.')
          const created: MockToken = { id: `token-${nextID++}`, name: input.name, createdAt: now(), expiresAt: new Date(Date.now() + (input.expiresInSeconds || 2_592_000) * 1000).toISOString() }
          tokens.push(created)
          return sendJSON(response, 201, { ...created, token: `zfm_demo_${randomBytes(24).toString('base64url')}` })
        }
      }

      const tokenMatch = path.match(/^\/api\/v1\/tokens\/([^/]+)$/)
      if (tokenMatch && method === 'DELETE') {
        const index = tokens.findIndex((candidate) => candidate.id === decodeURIComponent(tokenMatch[1] || ''))
        if (index < 0) return sendProblem(response, 404, 'Token not found.')
        tokens.splice(index, 1)
        return sendEmpty(response)
      }

      return sendProblem(response, 404, 'Mock endpoint not found.')
    } catch (error) {
      return sendProblem(response, 400, error instanceof Error ? error.message : 'Invalid mock request.')
    }
  }
  return (request, response, next) => {
    void handle(request, response, next)
  }
}

export function mockApiPlugin(): Plugin {
  return {
    name: 'zenfm-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(createMockApiMiddleware())
      server.config.logger.info('\n  ZenFM mock API is active')
      server.config.logger.info(`  Login: ${USERNAME} / ${DEMO_PASSWORD}`)
      server.config.logger.info('  Data resets when this Vite process stops; TUS and archive downloads are not simulated.\n')
    },
  }
}
