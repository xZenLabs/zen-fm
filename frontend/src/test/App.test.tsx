import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from './server'
import { renderApp } from './renderApp'

describe('authentication flow', () => {
  it('shows login and routes a temporary owner session to mandatory setup', async () => {
    let loginBody: Record<string, unknown> | undefined
    server.use(
      http.get('http://localhost/api/v1/session', () => HttpResponse.json({ title: 'Unauthorized', status: 401 }, { status: 401 })),
      http.post('http://localhost/api/v1/session', async ({ request }) => {
        loginBody = await request.json() as Record<string, unknown>
        return HttpResponse.json({ authenticated: true, setupRequired: true, csrfToken: 'x'.repeat(32) })
      }),
    )
    const user = userEvent.setup()
    renderApp('/login')

    const password = await screen.findByLabelText(/Password/)
    const heading = screen.getByRole('heading', { name: 'ZenFM' })
    expect(heading.parentElement?.querySelector('.zen-mark')).toHaveStyle({ width: '52px', height: '52px' })
    expect(document.querySelectorAll('.zen-mark')).toHaveLength(1)
    expect(screen.queryByLabelText(/Username/)).not.toBeInTheDocument()
    expect(password).toHaveAttribute('name', 'password')
    expect(password).toHaveAttribute('id', 'current-password')
    expect(password).toHaveAttribute('autocomplete', 'current-password')

    await user.type(password, 'temporary password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(loginBody).toEqual({ password: 'temporary password' })
    expect(await screen.findByRole('heading', { name: 'New Password' })).toBeInTheDocument()
    expect(screen.getByText('Replace the temporary password before accessing your files.')).toBeInTheDocument()
  })

  it('requires seven Unicode characters for the setup password', async () => {
    server.use(http.get('http://localhost/api/v1/session', () => HttpResponse.json({
      authenticated: true,
      setupRequired: true,
      csrfToken: 'unicode-password-csrf-value-12345',
    })))
    const user = userEvent.setup()
    renderApp('/setup')

    const newPassword = await screen.findByLabelText(/New password/)
    const confirmation = screen.getByLabelText(/Confirm password/)
    expect(screen.queryByLabelText(/Temporary password/)).not.toBeInTheDocument()
    expect(newPassword).toHaveAttribute('id', 'new-password')
    expect(newPassword).toHaveAttribute('name', 'new-password')
    expect(newPassword).toHaveAttribute('autocomplete', 'new-password')
    expect(confirmation).toHaveAttribute('id', 'confirm-password')
    expect(confirmation).toHaveAttribute('name', 'confirm-password')
    expect(confirmation).toHaveAttribute('autocomplete', 'new-password')

    await user.type(newPassword, '🌿'.repeat(6))
    await user.type(confirmation, '🌿'.repeat(6))
    expect(screen.getByRole('button', { name: 'Finish setup' })).toBeDisabled()

    await user.type(newPassword, '🌿')
    await user.type(confirmation, '🌿')
    expect(screen.getByRole('button', { name: 'Finish setup' })).toBeEnabled()
  })

  it('offers the permanent setup credential to the browser password manager', async () => {
    const stored = vi.fn((credential: Credential) => Promise.resolve(credential))
    class TestPasswordCredential {
      id: string
      password: string

      constructor({ id, password }: { id: string; password: string }) {
        this.id = id
        this.password = password
      }
    }
    const passwordCredentialDescriptor = Object.getOwnPropertyDescriptor(window, 'PasswordCredential')
    const credentialsDescriptor = Object.getOwnPropertyDescriptor(navigator, 'credentials')
    Object.defineProperty(window, 'PasswordCredential', { configurable: true, value: TestPasswordCredential })
    Object.defineProperty(navigator, 'credentials', { configurable: true, value: { store: stored } })

    let body: Record<string, unknown> | undefined
    server.use(
      http.get('http://localhost/api/v1/session', () => HttpResponse.json({
        authenticated: true,
        setupRequired: true,
        csrfToken: 'setup-password-csrf-value-123456',
      })),
      http.put('http://localhost/api/v1/owner/password', async ({ request }) => {
        body = await request.json() as Record<string, unknown>
        return HttpResponse.json({
          authenticated: true,
          setupRequired: false,
          csrfToken: 'replacement-csrf-value-1234567',
        })
      }),
    )

    try {
      const user = userEvent.setup()
      renderApp('/setup')
      await user.type(await screen.findByLabelText(/New password/), 'a permanent owner password')
      await user.type(screen.getByLabelText(/Confirm password/), 'a permanent owner password')
      await user.click(screen.getByRole('button', { name: 'Finish setup' }))

      await waitFor(() => expect(stored).toHaveBeenCalledTimes(1))
      expect(stored.mock.calls[0]?.[0]).toMatchObject({ id: 'owner', password: 'a permanent owner password' })
      expect(body).toEqual({ newPassword: 'a permanent owner password' })
      expect(await screen.findByRole('heading', { name: 'Files' })).toBeInTheDocument()
    } finally {
      if (passwordCredentialDescriptor) Object.defineProperty(window, 'PasswordCredential', passwordCredentialDescriptor)
      else Reflect.deleteProperty(window, 'PasswordCredential')
      if (credentialsDescriptor) Object.defineProperty(navigator, 'credentials', credentialsDescriptor)
      else Reflect.deleteProperty(navigator, 'credentials')
    }
  })
})

describe('responsive and accessible shell', () => {
  it('uses labelled mobile navigation and 44px touch controls', async () => {
    const media = vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      matches: query.includes('max-width'), media: query, onchange: null,
      addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    }))
    renderApp('/files')

    await screen.findByRole('heading', { name: 'Files' })
    expect(document.querySelector('.zen-mark')).toHaveAttribute('src', '/zen-fm.svg')
    expect(media).toHaveBeenCalledWith(expect.stringContaining('max-width'))
    const navigation = screen.getByRole('navigation', { name: 'Primary navigation' })
    const filesLink = within(navigation).getByRole('link', { name: 'Files' })
    expect(filesLink).toBeInTheDocument()
    const sharesLink = within(navigation).getByRole('link', { name: 'Shares' })
    expect(within(sharesLink).getByTestId('ShareIcon')).toBeInTheDocument()
    expect(within(navigation).getByRole('link', { name: 'Settings' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Grid view' })).toHaveAttribute('aria-pressed', 'true'))
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeInTheDocument()
    const upload = screen.getByRole('button', { name: 'Upload' })
    const newFile = screen.getByRole('button', { name: 'New file' })
    expect(getComputedStyle(upload).minHeight).toBe('44px')
    expect(upload).toHaveClass('MuiButton-contained', 'MuiButton-colorPrimary')
    expect(getComputedStyle(newFile).color).toBe('rgb(17, 17, 17)')
    expect(getComputedStyle(newFile.querySelector('svg')!)).toHaveProperty('color', 'rgb(13, 148, 136)')
    const nightMode = screen.getByRole('button', { name: 'Switch to dark mode' })
    expect(getComputedStyle(within(nightMode).getByTestId('DarkModeRoundedIcon')).color).toBe('rgba(0, 0, 0, 0.6)')
  })

  it('applies the owner theme restored from server settings', async () => {
    server.use(http.get('http://localhost/api/v1/settings', () => HttpResponse.json({
      theme: 'dark', locale: 'en', showHidden: false, clientTimeoutSeconds: 30,
      advancedMode: false, root: '/mnt/us', secureTransport: true,
    })))
    renderApp('/files')

    await screen.findByRole('heading', { name: 'Files' })
    await waitFor(() => expect(document.documentElement).toHaveAttribute('data-zenfm-theme', 'dark'))
  })

  it('follows the browser theme until the header toggle saves a manual choice', async () => {
    let saved: Record<string, unknown> | undefined
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      matches: query === '(prefers-color-scheme: dark)', media: query, onchange: null,
      addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    }))
    server.use(http.put('http://localhost/api/v1/settings', async ({ request }) => {
      saved = await request.json() as Record<string, unknown>
      return HttpResponse.json({
        theme: 'light', locale: 'en', showHidden: false, clientTimeoutSeconds: 30,
        advancedMode: false, root: '/mnt/us', secureTransport: true, version: 'test-backend',
      })
    }))
    const user = userEvent.setup()
    renderApp('/files')

    await screen.findByRole('heading', { name: 'Files' })
    await waitFor(() => expect(document.documentElement).toHaveAttribute('data-zenfm-theme', 'dark'))
    const signOut = screen.getByRole('button', { name: 'Sign out' })
    const themeToggle = screen.getByRole('button', { name: 'Switch to light mode' })
    expect(themeToggle.closest('.MuiToolbar-root')).toContainElement(signOut)

    await user.click(themeToggle)

    await waitFor(() => expect(saved).toEqual({ theme: 'light' }))
    expect(document.documentElement).toHaveAttribute('data-zenfm-theme', 'light')
  })

  it('shows the running backend version in settings', async () => {
    server.use(http.get('http://localhost/api/v1/settings', () => HttpResponse.json({
      theme: 'system', locale: 'en', showHidden: false, clientTimeoutSeconds: 30,
      advancedMode: false, root: '/mnt/us', secureTransport: true, version: '9.8.7-backend',
    })))
    renderApp('/settings')

    expect(await screen.findByText('Version: 9.8.7-backend')).toBeInTheDocument()
  })

  it('places the hidden-file preference in general settings', async () => {
    renderApp('/settings')

    expect(await screen.findByRole('heading', { name: 'General' })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Show hidden files' })).toBeInTheDocument()
  })
})
