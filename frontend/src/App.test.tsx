import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from './test/server'
import { renderApp } from './test/renderApp'

describe('authentication flow', () => {
  it('shows login and routes a temporary owner session to mandatory setup', async () => {
    server.use(
      http.get('http://localhost/api/v1/session', () => HttpResponse.json({ title: 'Unauthorized', status: 401 }, { status: 401 })),
      http.post('http://localhost/api/v1/session', () => HttpResponse.json({ authenticated: true, setupRequired: true, csrfToken: 'x'.repeat(32) })),
    )
    const user = userEvent.setup()
    renderApp('/login')

    const username = await screen.findByLabelText(/Username/)
    const password = screen.getByLabelText(/Password/)
    expect(username).toHaveAttribute('name', 'username')
    expect(username).toHaveAttribute('autocomplete', 'username')
    expect(password).toHaveAttribute('name', 'password')
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
      setupRequired: true,
      csrfToken: 'unicode-password-csrf-value-12345',
    })))
    const user = userEvent.setup()
    renderApp('/setup')

    await user.type(await screen.findByLabelText(/Temporary password/), 'temporary password')
    await user.type(screen.getByLabelText(/New password/), '🌿'.repeat(11))
    await user.type(screen.getByLabelText(/Confirm password/), '🌿'.repeat(11))
    expect(screen.getByRole('button', { name: 'Finish setup' })).toBeDisabled()

    await user.type(screen.getByLabelText(/New password/), '🌿')
    await user.type(screen.getByLabelText(/Confirm password/), '🌿')
    expect(screen.getByRole('button', { name: 'Finish setup' })).toBeEnabled()
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
    expect(within(navigation).getByRole('link', { name: 'Files' })).toBeInTheDocument()
    expect(within(navigation).getByRole('link', { name: 'Shares' })).toBeInTheDocument()
    expect(within(navigation).getByRole('link', { name: 'Settings' })).toBeInTheDocument()
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
})
