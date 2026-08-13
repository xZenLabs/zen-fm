import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from './server'
import { renderApp } from './renderApp'

describe('authentication flow', () => {
  it('shows login and routes a temporary owner session to mandatory setup', async () => {
    server.use(
      http.get('http://localhost/api/v1/session', () => HttpResponse.json({ title: 'Unauthorized', status: 401 }, { status: 401 })),
      http.post('http://localhost/api/v1/session', () => HttpResponse.json({ authenticated: true, username: 'koreader', setupRequired: true, csrfToken: 'x'.repeat(32) })),
    )
    const user = userEvent.setup()
    renderApp('/login')

    const username = await screen.findByLabelText(/Username/)
    const password = screen.getByLabelText(/Password/)
    const heading = screen.getByRole('heading', { name: 'ZenFM' })
    expect(heading.parentElement?.querySelector('.zen-mark')).toHaveStyle({ width: '52px', height: '52px' })
    expect(document.querySelectorAll('.zen-mark')).toHaveLength(1)
    expect(username).toHaveAttribute('name', 'username')
    expect(username).toHaveAttribute('id', 'username')
    expect(username).toHaveAttribute('autocomplete', 'username')
    expect(password).toHaveAttribute('name', 'password')
    expect(password).toHaveAttribute('id', 'current-password')
    expect(password).toHaveAttribute('autocomplete', 'current-password')

    await user.type(username, 'koreader')
    await user.type(password, 'temporary password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByRole('heading', { name: 'Choose a private password' })).toBeInTheDocument()
    expect(screen.getByText('Replace the temporary password before accessing your files.')).toBeInTheDocument()
  })

  it('counts Unicode code points when enforcing the 12-character setup password', async () => {
    server.use(http.get('http://localhost/api/v1/session', () => HttpResponse.json({
      authenticated: true,
      username: 'koreader',
      setupRequired: true,
      csrfToken: 'unicode-password-csrf-value-12345',
    })))
    const user = userEvent.setup()
    renderApp('/setup')

    const newPassword = await screen.findByLabelText(/New password/)
    const confirmation = screen.getByLabelText(/Confirm password/)
    const username = document.querySelector('input[name="username"]')
    expect(screen.queryByLabelText(/Temporary password/)).not.toBeInTheDocument()
    expect(username).toHaveAttribute('id', 'username')
    expect(username).toHaveAttribute('autocomplete', 'username')
    expect(username).toHaveValue('koreader')
    expect(newPassword).toHaveAttribute('id', 'new-password')
    expect(newPassword).toHaveAttribute('name', 'new-password')
    expect(newPassword).toHaveAttribute('autocomplete', 'new-password')
    expect(confirmation).toHaveAttribute('id', 'confirm-password')
    expect(confirmation).toHaveAttribute('name', 'confirm-password')
    expect(confirmation).toHaveAttribute('autocomplete', 'new-password')

    await user.type(newPassword, '🌿'.repeat(11))
    await user.type(confirmation, '🌿'.repeat(11))
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
        username: 'koreader',
        setupRequired: true,
        csrfToken: 'setup-password-csrf-value-123456',
      })),
      http.put('http://localhost/api/v1/owner/password', async ({ request }) => {
        body = await request.json() as Record<string, unknown>
        return HttpResponse.json({
          authenticated: true,
          username: 'koreader',
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
      expect(stored.mock.calls[0]?.[0]).toMatchObject({ id: 'koreader', password: 'a permanent owner password' })
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
    expect(within(navigation).getByRole('link', { name: 'Shares' })).toBeInTheDocument()
    expect(within(navigation).getByRole('link', { name: 'Settings' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Grid view' })).toHaveAttribute('aria-pressed', 'true'))
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeInTheDocument()
    expect(getComputedStyle(screen.getByRole('button', { name: 'Upload' })).minHeight).toBe('44px')
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
