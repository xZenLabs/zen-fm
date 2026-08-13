import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from './server'
import { renderApp } from './renderApp'

it('navigates nested public directories with capability-relative API paths', async () => {
  const requestedPaths: string[] = []
  server.use(http.get('http://localhost/api/v1/public/shares/test-secret', ({ request }) => {
    const relativePath = new URL(request.url).searchParams.get('path') ?? '/'
    requestedPaths.push(relativePath)
    if (relativePath === '/Nested') {
      return HttpResponse.json({
        name: 'Library', path: '/Nested',
        entries: [{ name: 'chapter.txt', path: '/Nested/chapter.txt', type: 'file', size: 12, modifiedAt: '2026-01-01T00:00:00Z' }],
      })
    }
    return HttpResponse.json({
      name: 'Library', path: '/',
      entries: [{ name: 'Nested', path: '/Nested', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }],
    })
  }))
  const user = userEvent.setup()
  renderApp('/s/test-secret')

  await user.click(await screen.findByRole('link', { name: 'Nested' }))
  expect(await screen.findByText('chapter.txt')).toBeInTheDocument()
  expect(requestedPaths).toContain('/')
  expect(requestedPaths).toContain('/Nested')
  expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute('href', expect.stringContaining('path=%2FNested%2Fchapter.txt'))

  await user.click(screen.getByRole('link', { name: 'Shared files' }))
  expect(await screen.findByRole('link', { name: 'Nested' })).toBeInTheDocument()
})

it('unlocks a password-protected share at the requested nested path', async () => {
  let unlockedPath = ''
  let suppliedPassword = ''
  server.use(
    http.get('http://localhost/api/v1/public/shares/protected-secret', () => HttpResponse.json({ name: 'Protected', path: '/', passwordRequired: true })),
    http.post('http://localhost/api/v1/public/shares/protected-secret', async ({ request }) => {
      unlockedPath = new URL(request.url).searchParams.get('path') ?? ''
      suppliedPassword = ((await request.json()) as { password: string }).password
      return HttpResponse.json({ name: 'Protected', path: '/Nested', entries: [] })
    }),
  )
  const user = userEvent.setup()
  renderApp('/s/protected-secret/Nested')

  await user.type(await screen.findByLabelText('Optional password'), 'quiet-secret')
  await user.click(screen.getByRole('button', { name: 'Unlock' }))
  expect(await screen.findByRole('link', { name: 'Nested' })).toBeInTheDocument()
  expect(unlockedPath).toBe('/Nested')
  expect(suppliedPassword).toBe('quiet-secret')
})
