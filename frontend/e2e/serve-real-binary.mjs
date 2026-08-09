import { spawn } from 'node:child_process'
import { cp, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import https from 'node:https'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const frontendDirectory = path.resolve(here, '..')
const repository = path.resolve(frontendDirectory, '..')
const temporary = await mkdtemp(path.join(os.tmpdir(), 'zenfm-e2e-'))
const source = path.join(temporary, 'source')
const children = []
let stopping = false

function port(name, fallback) {
  const value = Number(process.env[name] ?? fallback)
  if (!Number.isInteger(value) || value < 1024 || value > 65_535) throw new Error(`${name} must be a non-privileged TCP port`)
  return value
}

const ports = {
  normal: port('ZENFM_E2E_PORT', 18_780),
  setup: port('ZENFM_E2E_SETUP_PORT', 18_781),
  advanced: port('ZENFM_E2E_ADVANCED_PORT', 18_782),
  https: port('ZENFM_E2E_HTTPS_PORT', 18_783),
  expiry: port('ZENFM_E2E_EXPIRY_PORT', 18_784),
}

function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: 'inherit', ...options })
    child.once('error', reject)
    child.once('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`${command} exited with ${code ?? signal}`)))
  })
}

async function buildBinary() {
  if (process.env.ZENFM_E2E_BINARY) return path.resolve(process.env.ZENFM_E2E_BINARY)
  await mkdir(source, { recursive: true })
  await Promise.all(['cmd', 'internal', 'go.mod', 'go.sum'].map((entry) => cp(path.join(repository, entry), path.join(source, entry), { recursive: true })))
  const embedded = path.join(source, 'internal', 'webui', 'dist')
  await rm(embedded, { recursive: true, force: true })
  await cp(path.join(frontendDirectory, 'dist'), embedded, { recursive: true })
  const binary = path.join(temporary, 'zenfm')
  await run(process.env.GO ?? 'go', ['build', '-trimpath', '-buildvcs=false', '-o', binary, './cmd/zenfm'], { cwd: source })
  return binary
}

async function waitForHealth(url, child) {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`ZenFM exited before ${url} became healthy`)
    try {
      const healthy = url.startsWith('https:') ? await new Promise((resolve) => {
        const request = https.get(`${url}/healthz`, { rejectUnauthorized: false }, (response) => {
          response.resume()
          resolve(response.statusCode === 200)
        })
        request.once('error', () => resolve(false))
      }) : (await fetch(`${url}/healthz`)).ok
      if (healthy) return
    } catch {
      // The listener may not be ready yet.
    }
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error(`Timed out waiting for ${url}`)
}

async function start(binary, name, root, tcpPort, secure = false, extraArguments = []) {
  const data = path.join(temporary, `${name}-data`)
  await mkdir(data, { recursive: true })
  const child = spawn(binary, [
    'serve', ...(secure ? [] : ['--insecure-http']), '--listen', `127.0.0.1:${tcpPort}`, '--root', root,
    '--data-dir', data, '--control-socket', path.join(data, 'control.sock'), ...extraArguments,
  ], { stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.pipe(process.stdout)
  child.stderr.pipe(process.stderr)
  children.push(child)
  const url = `${secure ? 'https' : 'http'}://127.0.0.1:${tcpPort}`
  await waitForHealth(url, child)
  return url
}

async function configureOwner(url, password) {
  const login = await fetch(`${url}/api/v1/session`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Origin: url },
    body: JSON.stringify({ username: 'koreader', password: 'koreader123456789' }),
  })
  if (!login.ok) throw new Error(`Could not create E2E owner session: ${login.status}`)
  const cookie = login.headers.get('set-cookie')?.split(';', 1)[0]
  const session = await login.json()
  if (!cookie || typeof session.csrfToken !== 'string') throw new Error('E2E setup session omitted cookie or CSRF token')
  const change = await fetch(`${url}/api/v1/owner/password`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Cookie: cookie, Origin: url, 'X-ZenFM-CSRF': session.csrfToken },
    body: JSON.stringify({ currentPassword: 'koreader123456789', newPassword: password }),
  })
  if (!change.ok) throw new Error(`Could not configure E2E owner: ${change.status} ${await change.text()}`)
}

async function stop() {
  if (stopping) return
  stopping = true
  for (const child of children) if (child.exitCode === null) child.kill('SIGTERM')
  await Promise.all(children.map((child) => child.exitCode !== null ? undefined : new Promise((resolve) => {
    const force = setTimeout(() => child.kill('SIGKILL'), 5_000)
    child.once('exit', () => { clearTimeout(force); resolve() })
  })))
  await rm(temporary, { recursive: true, force: true })
}

try {
  const binary = await buildBinary()
  const normalRoot = path.join(temporary, 'normal-root')
  const setupRoot = path.join(temporary, 'setup-root')
  const httpsRoot = path.join(temporary, 'https-root')
  const expiryRoot = path.join(temporary, 'expiry-root')
  await mkdir(normalRoot, { recursive: true })
  await mkdir(setupRoot, { recursive: true })
  await mkdir(httpsRoot, { recursive: true })
  await mkdir(expiryRoot, { recursive: true })
  await writeFile(path.join(normalRoot, 'welcome.txt'), 'ZenFM E2E fixture\n')

  const normalURL = await start(binary, 'normal', normalRoot, ports.normal)
  await start(binary, 'setup', setupRoot, ports.setup)
  const advancedURL = await start(binary, 'advanced', '/', ports.advanced)
  await start(binary, 'https', httpsRoot, ports.https, true)
  const expiryURL = await start(binary, 'expiry', expiryRoot, ports.expiry, false, ['--session-idle', '10s', '--session-absolute', '4s'])
  await configureOwner(normalURL, 'zenfm-e2e-owner-password')
  await configureOwner(advancedURL, 'zenfm-e2e-owner-password')
  await configureOwner(expiryURL, 'zenfm-e2e-owner-password')
  process.stdout.write('ZenFM E2E servers ready\n')
} catch (error) {
  await stop()
  throw error
}

await new Promise((resolve) => {
  const finish = () => { void stop().finally(resolve) }
  process.once('SIGINT', finish)
  process.once('SIGTERM', finish)
})
