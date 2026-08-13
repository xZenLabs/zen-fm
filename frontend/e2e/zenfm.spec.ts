import { existsSync } from 'node:fs'
import { expect, request, test, type Locator, type Page } from '@playwright/test'

const unavailable = process.env.ZENFM_E2E_UNAVAILABLE === '1'
const normalURL = `http://127.0.0.1:${Number(process.env.ZENFM_E2E_PORT ?? 18_780)}`
const setupURL = `http://127.0.0.1:${Number(process.env.ZENFM_E2E_SETUP_PORT ?? 18_781)}`
const advancedURL = `http://127.0.0.1:${Number(process.env.ZENFM_E2E_ADVANCED_PORT ?? 18_782)}`
const httpsURL = `https://127.0.0.1:${Number(process.env.ZENFM_E2E_HTTPS_PORT ?? 18_783)}`
const expiryURL = `http://127.0.0.1:${Number(process.env.ZENFM_E2E_EXPIRY_PORT ?? 18_784)}`
const password = 'zenfm-e2e-owner-password'

async function login(page: Page, baseURL = normalURL, ownerPassword = password) {
  await page.goto(`${baseURL}/login`)
  await page.getByLabel('Username').fill('koreader')
  await page.getByLabel('Password').fill(ownerPassword)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page).toHaveURL(/\/files(?:\/|$)/)
}

async function expectLineNumbersAligned(container: Locator) {
  await expect(container.locator('.cm-lineNumbers')).toBeVisible()
  const lineBox = await container.locator('.cm-content .cm-line').first().boundingBox()
  const numberBox = await container.locator('.cm-lineNumbers .cm-gutterElement').last().boundingBox()
  expect(lineBox).not.toBeNull()
  expect(numberBox).not.toBeNull()
  expect(Math.abs((lineBox?.y ?? Number.POSITIVE_INFINITY) - (numberBox?.y ?? 0))).toBeLessThan(5)
}

test.describe('ZenFM real binary', () => {
  test.skip(unavailable, 'ZENFM_E2E_UNAVAILABLE=1 explicitly disables real-binary browser tests')

  test('forces setup-only sessions to replace the temporary password', async ({ page }) => {
    await page.goto(`${setupURL}/login`)
    await page.getByLabel('Username').fill('koreader')
    await page.getByLabel('Password').fill('koreader123456789')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(`${setupURL}/setup`)
    await expect(page.getByRole('heading', { name: 'Choose a private password' })).toBeVisible()

    const locked = await page.request.get(`${setupURL}/api/v1/files?path=/`)
    expect(locked.status()).toBe(403)

    await page.getByLabel('New password').fill('zenfm-e2e-setup-password')
    await page.getByLabel('Confirm password').fill('zenfm-e2e-setup-password')
    await page.getByRole('button', { name: 'Finish setup' }).click()
    await expect(page).toHaveURL(/\/files(?:\/|$)/)
  })

  test('logs the owner in and out without browser-stored bearer credentials', async ({ page }) => {
    await login(page)
    await page.getByRole('button', { name: 'Sign out' }).click()
    await expect(page).toHaveURL(`${normalURL}/login`)
    await login(page)
    expect(await page.evaluate(() => ({ local: Object.keys(localStorage), session: Object.keys(sessionStorage) })))
      .toEqual({ local: ['zenfm.files.sort'], session: [] })
  })

  test('expires a browser session at the server absolute deadline', async ({ page }) => {
    await login(page, expiryURL)
    await expect(page).toHaveURL(/\/files(?:\/|$)/)
    await expect(page).toHaveURL(`${expiryURL}/login`, { timeout: 8_000 })
    expect((await page.request.get(`${expiryURL}/api/v1/session`)).status()).toBe(401)
  })

  test('navigates and downloads from a nested public directory capability', async ({ page, browser }) => {
    await login(page)
    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByLabel('Folder name').fill('E2E Folder')
    await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click()
    await expect(page.getByText('E2E Folder', { exact: true })).toBeVisible()
    await page.getByText('E2E Folder', { exact: true }).dblclick()

    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByLabel('Folder name').fill('Nested')
    await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click()
    await page.getByText('Nested', { exact: true }).dblclick()

    await page.locator('input[type="file"]').setInputFiles({
      name: 'e2e-note.txt',
      mimeType: 'text/plain',
      buffer: Buffer.from('A quiet browser test.\n'),
    })
    await expect(page.getByText('e2e-note.txt', { exact: true })).toBeVisible()
    await page.getByRole('link', { name: 'Home' }).click()
    await page.getByLabel('Actions for E2E Folder').click()
    await page.getByRole('menuitem', { name: 'Share' }).click()
    await page.getByRole('dialog').getByLabel('Label').fill('E2E link')
    const createdResponse = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/shares'))
    await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click()
    const created = await (await createdResponse).json() as { url: string }

    await page.getByRole('link', { name: 'Shares' }).click()
    await expect(page.getByText('E2E link', { exact: true })).toBeVisible()

    const publicContext = await browser.newContext()
    const publicPage = await publicContext.newPage()
    await publicPage.goto(new URL(created.url, normalURL).toString())
    await expect(publicPage.getByRole('heading', { name: 'E2E link' })).toBeVisible()
    await publicPage.getByRole('link', { name: 'Nested' }).click()
    await expect(publicPage.getByText('e2e-note.txt', { exact: true })).toBeVisible()
    const rawResponse = publicPage.waitForResponse((response) => response.url().includes('/api/v1/public/shares/') && response.url().includes('/raw'))
    await publicPage.getByRole('link', { name: 'Download' }).click()
    expect(await (await rawResponse).text()).toBe('A quiet browser test.\n')
    await publicContext.close()
  })

  test('creates a personal token, uses it as Bearer, and revokes it', async ({ page }) => {
    await login(page)
    await page.getByRole('link', { name: 'Settings' }).click()
    await page.getByLabel('Token name').fill('E2E token')
    await page.getByRole('button', { name: 'Create token' }).click()
    const tokenDialog = page.getByRole('dialog', { name: 'Personal API token' })
    const token = await tokenDialog.locator('input').inputValue()
    expect(token).toMatch(/^zfm_pat_/)
    await tokenDialog.getByRole('button', { name: 'Close' }).click()

    const tokenClient = await request.newContext({ baseURL: normalURL, extraHTTPHeaders: { Authorization: `Bearer ${token}` } })
    expect((await tokenClient.get('/api/v1/files?path=/')).status()).toBe(200)
    const revoked = page.waitForResponse((response) => response.request().method() === 'DELETE' && response.url().includes('/api/v1/tokens/'))
    await page.getByRole('button', { name: 'Revoke' }).click()
    await revoked
    expect((await tokenClient.get('/api/v1/files?path=/')).status()).toBe(401)
    await tokenClient.dispose()
  })

  test('previews, edits, renames, copies, and deletes a text file', async ({ page }) => {
    await login(page)
    await page.getByRole('button', { name: 'New file' }).click()
    await page.getByRole('dialog', { name: 'New file' }).getByLabel('File name').fill('edit-flow.txt')
    await page.getByRole('dialog', { name: 'New file' }).getByRole('button', { name: 'Create' }).click()
    const editorDialog = page.getByRole('dialog', { name: 'Editing edit-flow.txt' })
    await expect(editorDialog).toBeVisible()
    await expectLineNumbersAligned(editorDialog)
    await page.locator('.cm-content').fill('Before edit')
    await page.getByRole('dialog').getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText('edit-flow.txt', { exact: true })).toBeVisible()
    await page.getByText('edit-flow.txt', { exact: true }).dblclick()
    await expect(page.getByText('Before edit', { exact: true })).toBeVisible()
    const previewDialog = page.getByRole('dialog')
    await expectLineNumbersAligned(previewDialog)
    await page.getByRole('dialog').getByRole('button', { name: 'Edit' }).click()
    await page.locator('.cm-content').fill('After edit')
    await page.getByRole('dialog').getByRole('button', { name: 'Save changes' }).click()
    await page.getByText('edit-flow.txt', { exact: true }).dblclick()
    await expect(page.getByText('After edit', { exact: true })).toBeVisible()
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click()

    await page.getByLabel('Actions for edit-flow.txt').click()
    await page.getByRole('menuitem', { name: 'Rename' }).click()
    await page.getByRole('dialog').getByLabel('Name').fill('renamed-flow.txt')
    await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click()
    await expect(page.getByText('renamed-flow.txt', { exact: true })).toBeVisible()

    await page.getByLabel('Actions for renamed-flow.txt').click()
    await page.getByRole('menuitem', { name: 'Copy' }).click()
    await page.getByRole('dialog').getByLabel('Destination path').fill('/copy-flow.txt')
    await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click()
    await expect(page.getByText('copy-flow.txt', { exact: true })).toBeVisible()

    await page.getByLabel('Actions for copy-flow.txt').click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('dialog', { name: 'Delete copy-flow.txt?' }).getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByText('copy-flow.txt', { exact: true })).toHaveCount(0)
  })

  test('uses bounded media previews and does not raster-preview SVG', async ({ page }) => {
    await login(page)
    await page.locator('input[type="file"]').setInputFiles([
      { name: 'quiet-e2e.mp3', mimeType: 'audio/mpeg', buffer: Buffer.from('ID3quiet') },
      { name: 'quiet-e2e.mp4', mimeType: 'video/mp4', buffer: Buffer.from('quiet video') },
      { name: 'vector-e2e.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"><text>quiet</text></svg>') },
    ])
    await expect(page.getByText('vector-e2e.svg', { exact: true })).toBeVisible()

    await page.getByText('quiet-e2e.mp3', { exact: true }).dblclick()
    const audioSource = await page.locator('audio').getAttribute('src')
    expect(audioSource).toContain('/api/v1/files/preview?path=')
    expect((await page.request.get(new URL(audioSource!, normalURL).toString())).status()).toBe(200)
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click()

    await page.getByText('quiet-e2e.mp4', { exact: true }).dblclick()
    const videoSource = await page.locator('video').getAttribute('src')
    expect(videoSource).toContain('/api/v1/files/preview?path=')
    expect((await page.request.get(new URL(videoSource!, normalURL).toString())).status()).toBe(200)
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click()

    await page.getByText('vector-e2e.svg', { exact: true }).dblclick()
    await expect(page.getByText('Preview is unavailable for this file.')).toBeVisible()
    await expect(page.getByRole('dialog').getByRole('img', { name: 'vector-e2e.svg' })).toHaveCount(0)
  })

  test('uploads a file larger than 8 MiB through resumable TUS', async ({ page }) => {
    await login(page)
    const created = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/uploads'))
    const patched = page.waitForResponse((response) => response.request().method() === 'PATCH' && response.url().includes('/api/v1/uploads/'))
    await page.locator('input[type="file"]').setInputFiles({
      name: 'large-e2e.txt',
      mimeType: 'text/plain',
      buffer: Buffer.alloc(8 * 1024 * 1024 + 1, 0x5a),
    })
    expect((await created).status()).toBe(201)
    expect((await patched).status()).toBe(204)
    await expect(page.getByText('large-e2e.txt', { exact: true })).toBeVisible()
    await page.getByLabel('Actions for large-e2e.txt').click()
    await expect(page.getByRole('menuitem', { name: 'Edit' })).toHaveCount(0)
  })

  test('refetches hidden entries after saving the general setting', async ({ page }) => {
    await login(page)
    const uploaded = page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().includes('path=%2F.hidden-e2e.txt'))
    await page.locator('input[type="file"]').setInputFiles({ name: '.hidden-e2e.txt', mimeType: 'text/plain', buffer: Buffer.from('quiet') })
    expect((await uploaded).ok()).toBe(true)
    await expect(page.getByText('.hidden-e2e.txt', { exact: true })).toHaveCount(0)
    await page.getByRole('link', { name: 'Settings' }).click()
    await page.getByRole('switch', { name: 'Show hidden files' }).click()
    const saved = page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().endsWith('/api/v1/settings'))
    await page.getByRole('button', { name: 'Save settings' }).click()
    expect((await saved).ok()).toBe(true)
    await page.getByRole('link', { name: 'Files' }).click()
    await expect(page.getByText('.hidden-e2e.txt', { exact: true })).toBeVisible()

    await page.getByRole('link', { name: 'Settings' }).click()
    await page.getByRole('switch', { name: 'Show hidden files' }).click()
    const reset = page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().endsWith('/api/v1/settings'))
    await page.getByRole('button', { name: 'Save settings' }).click()
    expect((await reset).ok()).toBe(true)
  })

  test('shows the explicit HTTP transport warning', async ({ page }) => {
    await login(page)
    await expect(page.getByText('This connection is using HTTP. Credentials and file contents may be visible on the network.')).toBeVisible()
  })

  test('keeps the advanced-root warning visible and lists the host pseudo-filesystems', async ({ page }) => {
    await login(page, advancedURL)
    const warning = page.getByText('Advanced root mode is active. System files, device paths, and ZenFM secrets are visible and may be changed or deleted.')
    await expect(warning).toBeVisible()
    await expect(page.getByText('dev', { exact: true })).toBeVisible()
    if (existsSync('/proc')) await expect(page.getByText('proc', { exact: true })).toBeVisible()
    await page.getByRole('link', { name: 'Shares' }).click()
    await expect(warning).toBeVisible()
  })

  test('serves the embedded app over a generated HTTPS certificate', async ({ page }) => {
    await page.goto(`${httpsURL}/login`)
    expect(new URL(page.url()).protocol).toBe('https:')
    await page.getByLabel('Username').fill('koreader')
    await page.getByLabel('Password').fill('koreader123456789')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await page.getByLabel('New password').fill('zenfm-e2e-https-password')
    await page.getByLabel('Confirm password').fill('zenfm-e2e-https-password')
    await page.getByRole('button', { name: 'Finish setup' }).click()
    await expect(page).toHaveURL(/\/files(?:\/|$)/)
    expect((await page.request.get(`${httpsURL}/healthz`)).status()).toBe(200)
    await expect(page.getByText('This connection is using HTTP. Credentials and file contents may be visible on the network.')).toHaveCount(0)
  })
})
