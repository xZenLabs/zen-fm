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
  await page.locator('input[name="password"]').fill(ownerPassword)
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
    await page.locator('input[name="password"]').fill('koreader123456789')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(`${setupURL}/setup`)
    await expect(page.getByRole('heading', { name: 'New Password' })).toBeVisible()

    const locked = await page.request.get(`${setupURL}/api/v1/files?path=/`)
    expect(locked.status()).toBe(403)

    await page.getByLabel('New password').fill('seven77')
    await page.getByLabel('Confirm password').fill('seven77')
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

  test('saves folder favorites in the navbar and moves excess links into an overflow menu as space changes', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 900 })
    await login(page)
    const session = await (await page.request.get(`${normalURL}/api/v1/session`)).json() as { csrfToken: string }
    const headers = { 'X-ZenFM-CSRF': session.csrfToken, Origin: normalURL }
    const names = ['Favorite Books #1', 'Favorite Documents', 'Favorite Photos', 'Favorite Music', 'Favorite Notes', 'Favorite Archive with a long name', 'Favorite Downloads']
    const favorites = page.getByRole('navigation', { name: 'Favorites', exact: true, includeHidden: true })
    const more = page.getByRole('button', { name: 'More favorites' })
    try {
      for (const name of names) {
        expect((await page.request.post(`${normalURL}/api/v1/files/directory`, { headers, data: { path: `/${name}` } })).status()).toBe(201)
      }
      await page.reload()
      await page.getByRole('row', { name: /Favorite Books #1/ }).click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Add to favorites' }).click()
      await expect(favorites.getByRole('link', { name: names[0], exact: true })).toBeVisible()
      await page.setViewportSize({ width: 320, height: 900 })
      await more.click()
      await expect(page.getByRole('menuitem', { name: /Favorite Books #1/ })).toBeVisible()
      await page.setViewportSize({ width: 1600, height: 900 })
      await expect(page.getByRole('menu')).toHaveCount(0)
      await page.setViewportSize({ width: 320, height: 900 })
      await expect(more).toHaveAttribute('aria-expanded', 'false')
      await page.setViewportSize({ width: 1600, height: 900 })
      await favorites.getByRole('link', { name: names[0], exact: true }).click()
      await expect(page).toHaveURL(`${normalURL}/files/Favorite%20Books%20%231`)
      await page.getByText('Nothing here yet', { exact: true }).click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Remove from favorites' }).click()
      await expect(favorites).toHaveCount(0)
      await page.getByText('Nothing here yet', { exact: true }).click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Add to favorites' }).click()
      await expect(favorites.getByRole('link', { name: names[0], exact: true })).toBeVisible()

      expect((await page.request.put(`${normalURL}/api/v1/settings`, { headers, data: { favorites: names.map((name) => `/${name}`) } })).status()).toBe(200)
      await page.reload()
      await expect(favorites.getByRole('link')).toHaveCount(5)
      await more.click()
      await expect(page.getByRole('menuitem')).toHaveCount(2)
      await page.getByRole('menuitem', { name: /Favorite Downloads/ }).click()
      await expect(page).toHaveURL(`${normalURL}/files/Favorite%20Downloads`)
      await expect(page.getByRole('menu')).toHaveCount(0)

      for (const width of [900, 600, 375, 320]) {
        await page.setViewportSize({ width, height: 900 })
        await expect.poll(() => favorites.getByRole('link').count()).toBeLessThan(5)
        await more.click()
        await expect.poll(async () => await favorites.getByRole('link', { includeHidden: true }).count() + await page.getByRole('menuitem').count()).toBe(names.length)
        await expect(page.getByRole('menuitem', { name: /Favorite Downloads/ })).toBeVisible()
        await page.keyboard.press('Escape')
        for (const item of await page.locator('header').locator('a, button').all()) {
          const bounds = await item.boundingBox()
          expect(bounds).not.toBeNull()
          expect(bounds!.x).toBeGreaterThanOrEqual(0)
          expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
        }
      }
      await page.setViewportSize({ width: 1600, height: 900 })
      await expect(favorites.getByRole('link')).toHaveCount(5)
      await page.getByRole('link', { name: 'Shares', exact: true }).click()
      await expect(favorites.getByRole('link')).toHaveCount(5)
      await favorites.getByRole('link', { name: names[0], exact: true }).click()
      await expect(page).toHaveURL(`${normalURL}/files/Favorite%20Books%20%231`)
      await page.getByRole('link', { name: 'Home', exact: true }).click()
      await page.getByRole('button', { name: `Actions for ${names[0]}`, exact: true }).click()
      await page.getByRole('menuitem', { name: 'Rename' }).click()
      const rename = page.getByRole('dialog', { name: 'Rename', exact: true })
      await rename.getByLabel('Name', { exact: true }).fill('Favorite Renamed #1')
      await rename.getByRole('button', { name: 'Confirm' }).click()
      const renamed = favorites.getByRole('link', { name: 'Favorite Renamed #1', exact: true })
      await expect(renamed).toHaveAttribute('href', '/files/Favorite%20Renamed%20%231')
      await expect(favorites.getByRole('link', { name: names[0], exact: true })).toHaveCount(0)
      await page.reload()
      await expect(renamed).toHaveAttribute('href', '/files/Favorite%20Renamed%20%231')
      await renamed.click()
      await expect(page).toHaveURL(`${normalURL}/files/Favorite%20Renamed%20%231`)
      await expect(page.getByText('Nothing here yet', { exact: true })).toBeVisible()
      await renamed.click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Edit label', exact: true }).click()
      await page.getByRole('dialog').getByLabel('Label', { exact: true }).fill('Reading & notes')
      await page.getByRole('dialog').getByRole('button', { name: 'Save changes', exact: true }).click()
      const labeled = favorites.getByRole('link', { name: 'Reading & notes', exact: true })
      await expect(labeled).toHaveAttribute('href', '/files/Favorite%20Renamed%20%231')
      await expect(page).toHaveURL(`${normalURL}/files/Favorite%20Renamed%20%231`)
      await page.reload()
      await expect(labeled).toHaveAttribute('href', '/files/Favorite%20Renamed%20%231')
      await labeled.focus()
      await page.keyboard.press('Shift+F10')
      await page.getByRole('menuitem', { name: 'Edit label', exact: true }).click()
      await page.getByRole('dialog').getByLabel('Label', { exact: true }).fill('Cancelled label')
      await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(labeled).toBeVisible()
      await page.setViewportSize({ width: 320, height: 900 })
      await more.click()
      const overflowFavorite = page.getByRole('menuitem', { name: /Reading & notes/ })
      await expect(overflowFavorite).toHaveAttribute('href', '/files/Favorite%20Renamed%20%231')
      await overflowFavorite.click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Remove from favorites', exact: true }).click()
      await expect(page.getByRole('menuitem', { name: 'Remove from favorites', exact: true })).toHaveCount(0)
      await more.click()
      await expect(page.getByRole('menuitem', { name: /Reading & notes/ })).toHaveCount(0)
      expect((await page.request.get(`${normalURL}/api/v1/files?path=/Favorite%20Renamed%20%231`)).status()).toBe(200)
    } finally {
      await page.request.put(`${normalURL}/api/v1/settings`, { headers, data: { favorites: [] } })
    }
  })

  test('opens file favorites from the navbar and overflow and follows file and parent renames', async ({ page }) => {
    await page.setViewportSize({ width: 1600, height: 900 })
    await login(page)
    const session = await (await page.request.get(`${normalURL}/api/v1/session`)).json() as { csrfToken: string }
    const headers = { 'X-ZenFM-CSRF': session.csrfToken, Origin: normalURL }
    const favorites = page.getByRole('navigation', { name: 'Favorites', exact: true })
    try {
      expect((await page.request.post(`${normalURL}/api/v1/files/directory`, { headers, data: { path: '/Favorite files' } })).status()).toBe(201)
      await page.goto(`${normalURL}/files/Favorite%20files`)
      await page.locator('input[type="file"]').setInputFiles({ name: 'note #1.txt', mimeType: 'text/plain', buffer: Buffer.from('Favorite file contents') })
      await page.getByRole('row', { name: /note #1.txt/ }).click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Add to favorites', exact: true }).click()
      const original = favorites.getByRole('link', { name: 'note #1.txt', exact: true })
      await expect(original).toHaveAttribute('href', '/files/Favorite%20files?file=note+%231.txt')
      await original.click()
      await expect(page.getByRole('dialog', { name: /^note #1\.txt/ })).toHaveClass(/MuiDialog-paperFullScreen/)
      await expect(page.getByText('Favorite file contents', { exact: true })).toBeVisible()
      await page.reload()
      await expect(page.getByText('Favorite file contents', { exact: true })).toBeVisible()
      await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()

      await page.getByRole('button', { name: 'Actions for note #1.txt', exact: true }).click()
      await page.getByRole('menuitem', { name: 'Rename', exact: true }).click()
      await page.getByRole('dialog').getByLabel('Name', { exact: true }).fill('note #2.txt')
      await page.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
      const renamed = favorites.getByRole('link', { name: 'note #2.txt', exact: true })
      await expect(renamed).toHaveAttribute('href', '/files/Favorite%20files?file=note+%232.txt')
      await page.getByRole('link', { name: 'Home', exact: true }).click()
      await page.getByRole('button', { name: 'Actions for Favorite files', exact: true }).click()
      await page.getByRole('menuitem', { name: 'Rename', exact: true }).click()
      await page.getByRole('dialog').getByLabel('Name', { exact: true }).fill('Renamed favorites')
      await page.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(renamed).toHaveAttribute('href', '/files/Renamed%20favorites?file=note+%232.txt')
      await page.reload()
      await expect(renamed).toHaveAttribute('href', '/files/Renamed%20favorites?file=note+%232.txt')

      await page.setViewportSize({ width: 320, height: 900 })
      await page.getByRole('button', { name: 'More favorites', exact: true }).click()
      await page.getByRole('menuitem', { name: /note #2.txt/ }).click()
      await expect(page.getByText('Favorite file contents', { exact: true })).toBeVisible()
      await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
      await page.getByRole('button', { name: 'More favorites', exact: true }).click()
      await page.getByRole('menuitem', { name: /note #2.txt/ }).click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Edit label', exact: true }).click()
      await page.getByRole('dialog').getByLabel('Label', { exact: true }).fill('My notes')
      await page.getByRole('dialog').getByRole('button', { name: 'Save changes', exact: true }).click()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      await page.getByRole('button', { name: 'More favorites', exact: true }).click()
      const labeled = page.getByRole('menuitem', { name: /My notes/ })
      await expect(labeled).toHaveAttribute('href', '/files/Renamed%20favorites?file=note+%232.txt')
      await labeled.click()
      await expect(page.getByText('Favorite file contents', { exact: true })).toBeVisible()
      await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
      await page.setViewportSize({ width: 1600, height: 900 })
      await favorites.getByRole('link', { name: 'My notes', exact: true }).click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Remove from favorites', exact: true }).click()
      await expect(favorites).toHaveCount(0)
      await page.reload()
      await expect(page.getByRole('button', { name: 'Actions for note #2.txt', exact: true })).toBeVisible()
      await expect(favorites).toHaveCount(0)
    } finally {
      await page.request.put(`${normalURL}/api/v1/settings`, { headers, data: { favorites: [] } })
    }
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
    await page.getByRole('dialog', { name: 'New file' }).getByLabel('File name').fill('edit-flow')
    await page.getByRole('dialog', { name: 'New file' }).getByRole('button', { name: 'Create' }).click()
    const editorDialog = page.getByRole('dialog', { name: 'Editing edit-flow' })
    await expect(editorDialog).toBeVisible()
    await expectLineNumbersAligned(editorDialog)
    await page.locator('.cm-content').fill('Before edit')
    await page.getByRole('dialog').getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText('edit-flow', { exact: true })).toBeVisible()
    await page.getByText('edit-flow', { exact: true }).dblclick()
    await expect(page.getByText('Before edit', { exact: true })).toBeVisible()
    const previewDialog = page.getByRole('dialog')
    await expectLineNumbersAligned(previewDialog)
    for (const modifier of ['Control', 'Meta']) {
      await previewDialog.getByRole('button', { name: 'Close' }).press(`${modifier}+a`)
      await expect(previewDialog.locator('.cm-content')).toBeFocused()
      expect(await page.evaluate<string>('window.getSelection().toString()')).toBe('Before edit')
    }
    await page.keyboard.press('Meta+f')
    const previewFindInput = previewDialog.getByRole('textbox', { name: 'Find in file' })
    await expect(previewFindInput).toBeFocused()
    await previewFindInput.pressSequentially('Before')
    await expect(previewDialog.getByText('1 of 1')).toBeVisible()
    await expect(previewDialog.locator('.cm-zen-find-current')).toHaveText('Before')
    await previewFindInput.press('ControlOrMeta+a')
    expect(await previewFindInput.evaluate((input) => [Number(Reflect.get(input, 'selectionStart')), Number(Reflect.get(input, 'selectionEnd'))])).toEqual([0, 6])
    await previewDialog.getByRole('button', { name: 'Open' }).click()
    await expect(previewDialog).toHaveClass(/MuiDialog-paperFullScreen/)
    await previewDialog.getByRole('button', { name: 'Close' }).press('Control+a')
    await expect(previewDialog.locator('.cm-content')).toBeFocused()
    expect(await page.evaluate<string>('window.getSelection().toString()')).toBe('Before edit')
    await page.keyboard.press('Control+f')
    const findInput = previewDialog.getByRole('textbox', { name: 'Find in file' })
    await expect(previewDialog.locator('.file-find-control')).toHaveClass(/open/)
    await expect(previewDialog.locator('.file-find-control')).toHaveCSS('width', '560px')
    await findInput.pressSequentially('Before')
    await expect(findInput).toBeFocused()
    await expect(findInput).toHaveValue('Before')
    await expect(previewDialog.getByText('1 of 1')).toBeVisible()
    await expect(previewDialog.locator('.cm-zen-find-current')).toHaveText('Before')
    await expect(previewDialog.locator('.cm-zen-find-match')).toHaveCount(1)
    await previewDialog.getByRole('button', { name: 'Clear find' }).click()
    await expect(findInput).toHaveValue('')
    await expect(findInput).toBeFocused()
    await expect(previewDialog.locator('.cm-zen-find-match')).toHaveCount(0)
    await page.getByRole('dialog').getByRole('button', { name: 'Edit' }).click()
    await page.locator('.cm-content').fill('After edit')
    await page.getByRole('dialog').getByRole('button', { name: 'Save changes' }).click()
    await page.getByText('edit-flow', { exact: true }).dblclick()
    await expect(page.getByText('After edit', { exact: true })).toBeVisible()
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click()

    await page.getByLabel('Actions for edit-flow').click()
    await page.getByRole('menuitem', { name: 'Rename' }).click()
    await page.getByRole('dialog').getByLabel('Name').fill('renamed-flow.txt')
    await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click()
    await expect(page.getByText('renamed-flow.txt', { exact: true })).toBeVisible()

    await page.getByLabel('Actions for renamed-flow.txt').click()
    await page.getByRole('menuitem', { name: 'Copy' }).click()
    await page.getByRole('button', { name: 'New folder' }).click()
    await page.getByRole('dialog', { name: 'New folder' }).getByLabel('Folder name').fill('copy-target')
    await page.getByRole('dialog', { name: 'New folder' }).getByRole('button', { name: 'Create' }).click()
    await page.getByText('copy-target', { exact: true }).dblclick()
    await page.getByText('Nothing here yet').click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Paste' }).click()
    await expect(page.getByText('renamed-flow.txt', { exact: true })).toBeVisible()

    await page.getByLabel('Actions for renamed-flow.txt').click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await page.getByRole('dialog', { name: 'Delete renamed-flow.txt?' }).getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByText('renamed-flow.txt', { exact: true })).toHaveCount(0)
    await page.getByRole('link', { name: 'Home' }).click()
    await expect(page.getByText('renamed-flow.txt', { exact: true })).toBeVisible()
  })

  test('finds text in rendered Markdown, HTML, and CSV previews', async ({ page }) => {
    await login(page)
    const files = [
      { name: 'find-preview.md', mimeType: 'text/markdown', buffer: Buffer.from('Al**pha** beta alpha') },
      { name: 'find-preview.html', mimeType: 'text/html', buffer: Buffer.from('<p>Al<strong>pha</strong> beta alpha</p>') },
      { name: 'find-preview.csv', mimeType: 'text/csv', buffer: Buffer.from('name,value\nAlpha,beta alpha') },
    ]
    await page.locator('input[type="file"]').setInputFiles(files)
    for (const file of files) {
      await page.getByText(file.name, { exact: true }).dblclick()
      const dialog = page.getByRole('dialog')
      await expect(dialog.getByText('beta alpha', { exact: false })).toBeVisible()
      await dialog.getByRole('button', { name: 'Close' }).press('Control+a')
      expect(await page.evaluate<string>('window.getSelection().toString()')).toContain('Alpha')
      await page.keyboard.press('Control+f')
      const findInput = dialog.getByRole('textbox', { name: 'Find in file' })
      await expect(findInput).toBeFocused()
      await findInput.pressSequentially('alpha')
      await expect(dialog.getByText('1 of 2')).toBeVisible()
      expect((await dialog.locator('.cm-zen-find-current').allTextContents()).join('')).toBe('Alpha')
      await findInput.press('Enter')
      await expect(dialog.getByText('2 of 2')).toBeVisible()
      await expect(dialog.locator('.cm-zen-find-current')).toHaveText('alpha')
      await expect(findInput).toBeFocused()
      await dialog.getByRole('button', { name: 'Clear find' }).click()
      await expect(dialog.locator('.cm-zen-find-match')).toHaveCount(0)
      await dialog.getByRole('button', { name: 'Close' }).click()
    }
  })

  test('uses bounded media and SVG previews', async ({ page }) => {
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
    const image = page.getByRole('dialog').getByRole('img', { name: 'vector-e2e.svg' })
    await expect(image).toBeVisible()
    await expect.poll(() => image.evaluate((element) => Number(Reflect.get(element, 'naturalWidth')))).toBeGreaterThan(0)
    const imageSource = await image.getAttribute('src')
    expect(imageSource).toContain('/api/v1/files/preview?path=')
    expect((await page.request.get(new URL(imageSource!, normalURL).toString())).status()).toBe(200)
    await page.getByRole('dialog').getByRole('button', { name: 'Open' }).click()
    await expect(page.getByRole('dialog')).toHaveClass(/MuiDialog-paperFullScreen/)
    await expect(image).toBeVisible()
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
    await page.locator('input[name="password"]').fill('koreader123456789')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await page.getByLabel('New password').fill('zenfm-e2e-https-password')
    await page.getByLabel('Confirm password').fill('zenfm-e2e-https-password')
    await page.getByRole('button', { name: 'Finish setup' }).click()
    await expect(page).toHaveURL(/\/files(?:\/|$)/)
    expect((await page.request.get(`${httpsURL}/health`)).status()).toBe(200)
    await expect(page.getByText('This connection is using HTTP. Credentials and file contents may be visible on the network.')).toHaveCount(0)
  })
})
