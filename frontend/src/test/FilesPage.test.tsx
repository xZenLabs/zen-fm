import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useLocation, useNavigate } from 'react-router-dom'
import { server } from './server'
import { renderApp, TestProviders } from './renderApp'
import { api } from '../api/client'
import { formatShortDate } from '../utils'
import App from '../App'

function RouterProbe() {
  const location = useLocation()
  const navigate = useNavigate()
  return <><output data-testid="route-location">{`${location.pathname}${location.search}`}</output><button onClick={() => void navigate(-1)}>Browser back</button></>
}

describe('file browser', () => {
  it('shows an icon-only clear action only while the search field has text', async () => {
    const user = userEvent.setup()
    renderApp('/files')

    const search = await screen.findByRole('textbox', { name: 'Search this folder' })
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument()
    await user.type(search, 'notes')
    const clear = screen.getByRole('button', { name: 'Clear search' })
    expect(clear).not.toHaveTextContent('Clear search')
    await user.click(clear)
    expect(search).toHaveValue('')
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument()
  })

  it('uses the saved hidden-file setting without a per-view toggle', async () => {
    const hiddenQueries: string[] = []
    const visibleEntries = [
      { name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
      { name: 'dev', path: '/dev', type: 'special', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
    ]
    const settings = { theme: 'system', locale: 'en', showHidden: true, clientTimeoutSeconds: 30, advancedMode: true, root: '/', secureTransport: true }
    server.use(
      http.get('http://localhost/api/v1/files', ({ request }) => {
        const hidden = new URL(request.url).searchParams.get('hidden') ?? ''
        hiddenQueries.push(hidden)
        return HttpResponse.json({
          path: '/', advancedMode: true,
          entries: hidden === 'true' ? [...visibleEntries, { name: '.zenfm.db', path: '/.zenfm.db', type: 'file', size: 512, modifiedAt: '2026-01-01T00:00:00Z', hidden: true }] : visibleEntries,
        })
      }),
      http.get('http://localhost/api/v1/settings', () => HttpResponse.json(settings)),
    )
    renderApp('/files')

    expect(await screen.findByText('.zenfm.db')).toBeInTheDocument()
    expect(screen.getByText('Books')).toBeInTheDocument()
    expect(screen.getByText('Advanced root mode is active. System files, device paths, and ZenFM secrets are visible and may be changed or deleted.')).toBeInTheDocument()
    expect(hiddenQueries).toContain('true')
    expect(screen.queryByRole('switch', { name: 'Show hidden files' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Actions for dev' })).toBeInTheDocument()
  })

  it('sends CSRF when creating a directory and refreshes the listing', async () => {
    let csrf = ''
    let created = false
    server.use(
      http.get('http://localhost/api/v1/session', () => HttpResponse.json({ authenticated: true, setupRequired: false, csrfToken: 'directory-csrf-token-value-1234567' })),
      http.get('http://localhost/api/v1/files', () => HttpResponse.json({ path: '/', advancedMode: false, entries: created ? [{ name: 'Notes', path: '/Notes', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }] : [] })),
      http.post('http://localhost/api/v1/files/directory', ({ request }) => {
        csrf = request.headers.get('X-ZenFM-CSRF') ?? ''
        created = true
        return new HttpResponse(null, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    await user.click(screen.getByRole('button', { name: 'New folder' }))
    await user.type(screen.getByLabelText('Folder name'), 'Notes')
    await user.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(csrf).toBe('directory-csrf-token-value-1234567'))
    expect(await screen.findByText('Notes')).toBeInTheDocument()
  })

  it('starts in list view, opens only on double click, and shows actions on right click', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [
        { name: 'manual.bin', path: '/manual.bin', type: 'file', size: 512, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'notes.bin', path: '/notes.bin', type: 'file', size: 256, modifiedAt: '2026-01-01T00:00:00Z' },
      ],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    const item = await screen.findByRole('row', { name: /manual\.bin/ })
    const listView = screen.getByRole('button', { name: 'List view' })
    const gridView = screen.getByRole('button', { name: 'Grid view' })
    expect(listView).toHaveAttribute('aria-pressed', 'true')
    await user.hover(listView)
    expect(await screen.findByRole('tooltip', { name: 'List view' })).toBeInTheDocument()
    await user.unhover(listView)
    await user.hover(gridView)
    expect(await screen.findByRole('tooltip', { name: 'Grid view' })).toBeInTheDocument()
    await user.unhover(gridView)
    await user.click(item)
    expect(item).toHaveClass('selected')
    const otherItem = screen.getByRole('row', { name: /notes\.bin/ })
    await user.click(otherItem)
    expect(item).not.toHaveClass('selected')
    expect(otherItem).toHaveClass('selected')
    await user.click(item)
    expect(screen.queryByRole('dialog', { name: 'manual.bin' })).not.toBeInTheDocument()

    fireEvent.contextMenu(item, { clientX: 246, clientY: 135 })
    const menu = screen.getByRole('menu')
    expect(menu).toBeInTheDocument()
    expect(menu.parentElement).toHaveStyle({ top: '135px', left: '246px' })
    const openItem = screen.getByRole('menuitem', { name: 'Open' })
    expect(openItem).toBeInTheDocument()
    expect(getComputedStyle(openItem.querySelector('.MuiListItemIcon-root')!)).toHaveProperty('color', 'rgb(13, 148, 136)')
    expect(screen.getByRole('menuitem', { name: 'Rename' })).toBeInTheDocument()
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete' })
    expect(getComputedStyle(deleteItem)).toHaveProperty('color', 'rgb(211, 47, 47)')
    expect(getComputedStyle(deleteItem.querySelector('svg')!)).toHaveProperty('color', 'rgb(211, 47, 47)')
    await user.keyboard('{Escape}')

    await user.dblClick(item)
    expect(await screen.findByRole('dialog', { name: 'manual.bin' })).toBeInTheDocument()
  })

  it('opens a highlighted file preview with Enter', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [{ name: 'manual.bin', path: '/manual.bin', type: 'file', size: 512, modifiedAt: '2026-01-01T00:00:00Z' }],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    await user.click(await screen.findByRole('button', { name: 'Grid view' }))
    const item = await screen.findByRole('listitem', { name: 'manual.bin' })
    const actionArea = item.querySelector<HTMLElement>('.MuiCardActionArea-root')!
    await user.click(actionArea)
    expect(item).toHaveClass('selected')
    expect(actionArea).toHaveFocus()

    await user.keyboard('{Enter}')

    expect(await screen.findByRole('dialog', { name: 'manual.bin' })).toBeInTheDocument()
  })

  it('selects a focused grid item before Enter opens it', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [
        { name: 'alpha.bin', path: '/alpha.bin', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'bravo.bin', path: '/bravo.bin', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
      ],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    await user.click(await screen.findByRole('button', { name: 'Grid view' }))
    const alpha = await screen.findByRole('listitem', { name: 'alpha.bin' })
    const bravo = screen.getByRole('listitem', { name: 'bravo.bin' })
    await user.click(alpha.querySelector<HTMLElement>('.MuiCardActionArea-root')!)
    bravo.querySelector<HTMLElement>('.MuiCardActionArea-root')!.focus()

    await user.keyboard('{Enter}')

    expect(alpha).not.toHaveClass('selected')
    expect(bravo).toHaveClass('selected')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    await user.keyboard('{Enter}')
    expect(await screen.findByRole('dialog', { name: 'bravo.bin' })).toBeInTheDocument()
  })

  it('enters a highlighted folder with Enter', async () => {
    server.use(http.get('http://localhost/api/v1/files', ({ request }) => {
      const path = new URL(request.url).searchParams.get('path')
      return HttpResponse.json(path === '/Books'
        ? { path, advancedMode: false, entries: [{ name: 'chapter.txt', path: '/Books/chapter.txt', type: 'file', size: 12, modifiedAt: '2026-01-01T00:00:00Z' }] }
        : { path: '/', advancedMode: false, entries: [{ name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }] })
    }))
    const user = userEvent.setup()
    renderApp('/files')

    const folder = await screen.findByRole('row', { name: /Books/ })
    await user.click(folder)
    expect(folder).toHaveClass('selected')

    await user.keyboard('{Enter}')

    expect(await screen.findByText('chapter.txt')).toBeInTheDocument()
  })

  it('prompts to delete all highlighted files and folders with Delete', async () => {
    let deletions = 0
    server.use(
      http.get('http://localhost/api/v1/files', () => HttpResponse.json({
        path: '/', advancedMode: false,
        entries: [
          { name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
          { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        ],
      })),
      http.delete('http://localhost/api/v1/files', () => {
        deletions++
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    renderApp('/files')

    const books = await screen.findByRole('row', { name: /Books/ })
    const notes = screen.getByRole('row', { name: /notes\.txt/ })
    fireEvent.click(books)
    fireEvent.click(notes, { ctrlKey: true })
    await user.keyboard('{Delete}')

    const dialog = screen.getByRole('dialog', { name: 'Delete 2 items?' })
    expect(dialog).toHaveTextContent('2 items selected')
    expect(deletions).toBe(0)
  })

  it('selects ranges with shift-click and toggles items with ctrl/cmd-click', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [
        { name: 'alpha.txt', path: '/alpha.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'bravo.txt', path: '/bravo.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'charlie.txt', path: '/charlie.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'delta.txt', path: '/delta.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
      ],
    })))
    renderApp('/files')

    const alpha = await screen.findByRole('row', { name: /alpha\.txt/ })
    const bravo = screen.getByRole('row', { name: /bravo\.txt/ })
    const charlie = screen.getByRole('row', { name: /charlie\.txt/ })
    const delta = screen.getByRole('row', { name: /delta\.txt/ })
    fireEvent.click(alpha)
    fireEvent.click(charlie, { shiftKey: true })

    expect(alpha).toHaveClass('selected')
    expect(bravo).toHaveClass('selected')
    expect(charlie).toHaveClass('selected')

    fireEvent.click(bravo, { ctrlKey: true })
    fireEvent.click(delta, { metaKey: true })
    expect(bravo).not.toHaveClass('selected')
    expect(delta).toHaveClass('selected')
  })

  it('shows coarse-input checkboxes before file and folder icons for multi-selection', async () => {
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      matches: query === '(pointer: coarse)', media: query, onchange: null,
      addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    }))
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: true,
      entries: [
        { name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'socket', path: '/run/socket', type: 'special', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
      ],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    const books = await screen.findByRole('row', { name: /Books/ })
    const notes = screen.getByRole('row', { name: /notes\.txt/ })
    const booksCheckbox = within(books).getByRole('checkbox', { name: 'Books' })
    const notesCheckbox = within(notes).getByRole('checkbox', { name: 'notes.txt' })
    expect(books.querySelector('.MuiCheckbox-root')?.nextElementSibling).toBe(within(books).getByTestId('FolderRoundedIcon'))
    expect(notes.querySelector('.MuiCheckbox-root')?.nextElementSibling).toBe(within(notes).getByTestId('InsertDriveFileRoundedIcon'))
    expect(within(screen.getByRole('row', { name: /socket/ })).queryByRole('checkbox')).not.toBeInTheDocument()

    await user.click(booksCheckbox)
    await user.click(notesCheckbox)
    expect(booksCheckbox).toBeChecked()
    expect(notesCheckbox).toBeChecked()
    expect(books).toHaveClass('selected')
    expect(notes).toHaveClass('selected')

    await user.click(screen.getByRole('button', { name: 'Grid view' }))
    const booksCard = screen.getByRole('listitem', { name: 'Books' })
    expect(booksCard.querySelector('.MuiCheckbox-root')?.nextElementSibling).toBe(within(booksCard).getByTestId('FolderRoundedIcon'))
    expect(within(booksCard).getByRole('checkbox', { name: 'Books' })).toBeChecked()
  })

  it('moves and deletes every selected item and shows the selection count in each dialog', async () => {
    const moves: Array<{ source: string; destination: string; overwrite: boolean }> = []
    const deletions: string[] = []
    server.use(
      http.get('http://localhost/api/v1/files', ({ request }) => {
        const path = new URL(request.url).searchParams.get('path')
        return HttpResponse.json(path === '/'
          ? {
              path, advancedMode: false,
              entries: [
                { name: 'alpha.txt', path: '/alpha.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
                { name: 'bravo.txt', path: '/bravo.txt', type: 'file', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
                { name: 'Archive', path: '/Archive', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
              ],
            }
          : { path, advancedMode: false, entries: [] })
      }),
      http.post('http://localhost/api/v1/files/move', async ({ request }) => {
        moves.push(await request.json() as { source: string; destination: string; overwrite: boolean })
        return new HttpResponse(null, { status: 204 })
      }),
      http.delete('http://localhost/api/v1/files', ({ request }) => {
        deletions.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    renderApp('/files')

    const alpha = await screen.findByRole('row', { name: /alpha\.txt/ })
    const bravo = screen.getByRole('row', { name: /bravo\.txt/ })
    expect(alpha).toHaveStyle({ cursor: 'pointer' })
    fireEvent.click(alpha)
    fireEvent.click(bravo, { ctrlKey: true })
    expect(screen.queryByText('2 selected')).not.toBeInTheDocument()

    fireEvent.contextMenu(bravo, { clientX: 200, clientY: 100 })
    const menu = screen.getByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: 'Download 2 items' })).toBeInTheDocument()
    expect(within(menu).getByRole('menuitem', { name: 'Copy 2 items' })).toBeInTheDocument()
    expect(within(menu).getByRole('menuitem', { name: 'Delete 2 items' })).toBeInTheDocument()
    await user.click(within(menu).getByRole('menuitem', { name: 'Move 2 items' }))
    const moveDialog = screen.getByRole('dialog', { name: 'Move 2 items' })
    await user.click(await within(moveDialog).findByRole('button', { name: 'Archive' }))
    await user.click(within(moveDialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(moves).toEqual([
      { source: '/alpha.txt', destination: '/Archive/alpha.txt', overwrite: false },
      { source: '/bravo.txt', destination: '/Archive/bravo.txt', overwrite: false },
    ]))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    fireEvent.click(alpha)
    fireEvent.click(bravo, { metaKey: true })
    fireEvent.contextMenu(bravo, { clientX: 200, clientY: 100 })
    await user.click(within(screen.getByRole('menu')).getByRole('menuitem', { name: 'Delete 2 items' }))
    const deleteDialog = screen.getByRole('dialog', { name: 'Delete 2 items?' })
    expect(deleteDialog).toHaveTextContent('2 items selected')
    await user.click(within(deleteDialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deletions).toEqual(['/alpha.txt', '/bravo.txt']))
  })

  it('shows current-folder creation actions when right-clicking outside the file table', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [{ name: 'notes.txt', path: '/notes.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' }],
    })))
    renderApp('/files')

    const row = await screen.findByRole('row', { name: /notes\.txt/ })
    expect(fireEvent.contextMenu(document.body, { clientX: 246, clientY: 135 })).toBe(false)

    expect(screen.getByRole('menuitem', { name: 'New file' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'New folder' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Rename' })).not.toBeInTheDocument()

    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
    fireEvent.contextMenu(row, { clientX: 200, clientY: 100 })
    expect(screen.getAllByRole('menuitem').map((item) => item.textContent)).toEqual([
      'Open',
      'Edit',
      'Rename',
      'Move',
      'Copy',
      'Download',
      'Share',
      'Checksum',
      'Delete',
    ])
    expect(within(screen.getByRole('menuitem', { name: 'Edit' })).getByTestId('EditDocumentIcon')).toBeInTheDocument()
    expect(within(screen.getByRole('menuitem', { name: 'Rename' })).getByTestId('EditRoundedIcon')).toBeInTheDocument()
    expect(within(screen.getByRole('menuitem', { name: 'Share' })).getByTestId('ShareIcon')).toBeInTheDocument()
  })

  it('copies an item to the app clipboard and pastes it into a right-clicked directory', async () => {
    const copies: Array<{ source: string; destination: string; overwrite: boolean }> = []
    server.use(
      http.get('http://localhost/api/v1/files', ({ request }) => {
        const requestedPath = new URL(request.url).searchParams.get('path')
        return HttpResponse.json(requestedPath === '/Archive'
          ? { path: '/Archive', advancedMode: false, entries: [] }
          : {
              path: '/', advancedMode: false,
              entries: [
                { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' },
                { name: 'Archive', path: '/Archive', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
              ],
            })
      }),
      http.post('http://localhost/api/v1/files/copy-size', () => HttpResponse.json({ items: [{ source: '/notes.txt', bytes: 8 }], totalBytes: 8 })),
      http.post('http://localhost/api/v1/files/copy', async ({ request }) => {
        copies.push(await request.json() as { source: string; destination: string; overwrite: boolean })
        return new HttpResponse('{"copiedBytes":8,"done":true}\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
      }),
    )
    const user = userEvent.setup()
    renderApp('/files')

    const source = await screen.findByRole('row', { name: /notes\.txt/ })
    const destination = screen.getByRole('row', { name: /Archive/ })
    fireEvent.contextMenu(destination, { clientX: 200, clientY: 100 })
    expect(screen.queryByRole('menuitem', { name: 'Paste' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')

    fireEvent.contextMenu(source, { clientX: 200, clientY: 100 })
    await user.click(screen.getByRole('menuitem', { name: 'Copy' }))
    expect(copies).toEqual([])
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    fireEvent.contextMenu(destination, { clientX: 200, clientY: 100 })
    expect(screen.getByRole('menuitem', { name: 'Paste' })).toBeInTheDocument()
    await user.keyboard('{Escape}')

    await user.dblClick(destination)
    const emptyFolder = await screen.findByText('Nothing here yet')
    fireEvent.contextMenu(emptyFolder, { clientX: 200, clientY: 100 })
    expect(screen.getByRole('menuitem', { name: 'New file' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'New folder' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Paste' }))

    await waitFor(() => expect(copies).toEqual([{ source: '/notes.txt', destination: '/Archive/notes.txt', overwrite: false }]))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Paste' })).not.toBeInTheDocument())
  })

  it('opens folders on double click', async () => {
    server.use(http.get('http://localhost/api/v1/files', ({ request }) => {
      const path = new URL(request.url).searchParams.get('path')
      return HttpResponse.json(path === '/Books'
        ? { path, advancedMode: false, entries: [{ name: 'chapter.txt', path: '/Books/chapter.txt', type: 'file', size: 12, modifiedAt: '2026-01-01T00:00:00Z' }] }
        : { path: '/', advancedMode: false, entries: [{ name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }] })
    }))
    const user = userEvent.setup()
    renderApp('/files')

    const folder = await screen.findByRole('row', { name: /Books/ })
    expect(within(folder).queryByText('folder')).not.toBeInTheDocument()
    expect(within(folder).queryByText('directory')).not.toBeInTheDocument()
    await user.click(folder)
    expect(screen.queryByText('chapter.txt')).not.toBeInTheDocument()
    await user.dblClick(folder)
    expect(await screen.findByText('chapter.txt')).toBeInTheDocument()
  })

  it('uploads host files to the shown folder or a hovered child folder', async () => {
    const uploadedPaths: string[] = []
    server.use(
      http.get('http://localhost/api/v1/files', () => HttpResponse.json({
        path: '/', advancedMode: false,
        entries: [{ name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }],
      })),
      http.put('*/api/v1/files/content', ({ request }) => {
        uploadedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderApp('/files')

    const folder = await screen.findByRole('row', { name: /Books/ })
    const rootTransfer = { types: ['Files'], files: [new File(['root'], 'root.txt', { type: 'text/plain' })], dropEffect: 'none' }
    fireEvent.dragOver(window, { dataTransfer: rootTransfer })
    expect(document.querySelector('.file-drop-zone')).toHaveClass('drop-active')
    fireEvent.drop(window, { dataTransfer: rootTransfer })
    await waitFor(() => expect(uploadedPaths).toContain('/root.txt'))

    const folderTransfer = { types: ['Files'], files: [new File(['nested'], 'nested.txt', { type: 'text/plain' })], dropEffect: 'none' }
    fireEvent.dragOver(folder, { dataTransfer: folderTransfer })
    expect(folder).toHaveClass('drop-target')
    fireEvent.drop(folder, { dataTransfer: folderTransfer })
    await waitFor(() => expect(uploadedPaths).toContain('/Books/nested.txt'))
    expect(folder).not.toHaveClass('drop-target')
  })

  it('shows total bytes, percentage, and ETA for an upload batch', async () => {
    let finishSecond: () => void = () => undefined
    vi.spyOn(api.files, 'uploadWithProgress').mockImplementation(async (path, file, _overwrite, onProgress) => {
      if (path === '/alpha.bin') {
        await new Promise((resolve) => setTimeout(resolve, 600))
        onProgress(file.size, file.size)
        return
      }
      await new Promise<void>((resolve) => {
        finishSecond = () => { onProgress(file.size, file.size); resolve() }
      })
    })
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, [new File(['aaaa'], 'alpha.bin'), new File(['bbbb'], 'bravo.bin')])

    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 650)) })
    expect(screen.getByText('Uploading · 1 of 2 files complete · alpha.bin')).toBeInTheDocument()
    expect(screen.getByText('4 B of 8 B — 50%')).toBeInTheDocument()
    expect(screen.getByText(/About \d+ seconds remaining/)).toBeInTheDocument()
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 1_100)) })
    expect(screen.getByText('About 1 second remaining')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'Total upload progress' })).toHaveAttribute('aria-valuenow', '50')
    await act(async () => {
      finishSecond()
      await Promise.resolve()
    })
    await waitFor(() => expect(screen.queryByRole('progressbar', { name: 'Total upload progress' })).not.toBeInTheDocument())
  })

  it('runs four uploads at a time, aborts active uploads, and does not start queued files when cancelled', async () => {
    const started: string[] = []
    const aborted: string[] = []
    vi.spyOn(api.files, 'uploadWithProgress').mockImplementation((path, _file, _overwrite, _onProgress, signal) => new Promise<void>((_resolve, reject) => {
      started.push(path)
      const abort = () => {
        aborted.push(path)
        reject(signal?.reason instanceof Error ? signal.reason : new DOMException('Aborted', 'AbortError'))
      }
      if (signal?.aborted) abort()
      else signal?.addEventListener('abort', abort, { once: true })
    }))
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, [
      new File(['one'], 'one.txt'),
      new File(['two'], 'two.txt'),
      new File(['three'], 'three.txt'),
      new File(['four'], 'four.txt'),
      new File(['five'], 'five.txt'),
    ])
    await waitFor(() => expect(started).toEqual(['/one.txt', '/two.txt', '/three.txt', '/four.txt']))

    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('progressbar', { name: 'Total upload progress' })).not.toBeInTheDocument())
    expect(aborted).toEqual(['/one.txt', '/two.txt', '/three.txt', '/four.txt'])
    expect(started).not.toContain('/five.txt')
    expect(screen.getByRole('button', { name: 'Upload' })).toBeEnabled()
    expect(screen.queryByText('The operation was aborted.')).not.toBeInTheDocument()
  })

  it('preserves a dropped directory tree instead of uploading the directory as a file', async () => {
    const createdDirectories: string[] = []
    const uploadedPaths: string[] = []
    server.use(
      http.post('http://localhost/api/v1/files/directory', async ({ request }) => {
        const body = await request.json() as { path: string }
        createdDirectories.push(body.path)
        return new HttpResponse(null, { status: 201 })
      }),
      http.put('*/api/v1/files/content', ({ request }) => {
        uploadedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 201 })
      }),
    )
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const fileEntry = (file: File): FileSystemFileEntry => ({
      isFile: true, isDirectory: false, name: file.name, fullPath: `/${file.name}`,
      file: (success) => success(file),
    } as FileSystemFileEntry)
    const directoryEntry = (name: string, children: FileSystemEntry[]): FileSystemDirectoryEntry => ({
      isFile: false, isDirectory: true, name, fullPath: `/${name}`,
      createReader: () => {
        let read = false
        return { readEntries: (success) => { const batch = read ? [] : children; read = true; success(batch) } }
      },
    } as FileSystemDirectoryEntry)
    const assets = directoryEntry('assets', [fileEntry(new File(['icon'], 'icon.png', { type: 'image/png' }))])
    const plugin = directoryEntry('zenfm.koplugin', [
      fileEntry(new File(['return {}'], 'main.lua', { type: 'text/plain' })),
      assets,
      directoryEntry('empty', []),
    ])
    const transfer = {
      types: ['Files'], files: [], dropEffect: 'none',
      items: [{ kind: 'file', webkitGetAsEntry: () => plugin }],
    }

    fireEvent.drop(window, { dataTransfer: transfer })

    await waitFor(() => expect(uploadedPaths).toHaveLength(2))
    expect(createdDirectories).toEqual([
      '/zenfm.koplugin',
      '/zenfm.koplugin/assets',
      '/zenfm.koplugin/empty',
    ])
    expect(uploadedPaths).toEqual([
      '/zenfm.koplugin/main.lua',
      '/zenfm.koplugin/assets/icon.png',
    ])
  })

  it('offers file and folder pickers and preserves paths from a selected folder', async () => {
    const createdDirectories: string[] = []
    const uploadedPaths: string[] = []
    server.use(
      http.post('http://localhost/api/v1/files/directory', async ({ request }) => {
        createdDirectories.push(((await request.json()) as { path: string }).path)
        return new HttpResponse(null, { status: 201 })
      }),
      http.put('*/api/v1/files/content', ({ request }) => {
        uploadedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.click(screen.getByRole('button', { name: 'Upload' }))
    expect(screen.getByRole('menuitem', { name: 'Upload files' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Upload files' }))
    expect(input.webkitdirectory).toBe(false)
    await user.click(screen.getByRole('button', { name: 'Upload' }))
    await user.click(screen.getByRole('menuitem', { name: 'Upload folder' }))

    expect(input.webkitdirectory).toBe(true)
    const chapter = new File(['chapter'], 'chapter.txt', { type: 'text/plain' })
    const cover = new File(['cover'], 'cover.png', { type: 'image/png' })
    Object.defineProperty(chapter, 'webkitRelativePath', { value: 'Books/chapter.txt' })
    Object.defineProperty(cover, 'webkitRelativePath', { value: 'Books/images/cover.png' })
    fireEvent.change(input, { target: { files: [chapter, cover] } })

    await waitFor(() => expect(uploadedPaths).toHaveLength(2))
    expect(input.webkitdirectory).toBe(false)
    expect(createdDirectories).toEqual(['/Books', '/Books/images'])
    expect(uploadedPaths.sort()).toEqual(['/Books/chapter.txt', '/Books/images/cover.png'])
  })

  it('confirms a table drag before moving a file into a folder', async () => {
    const moves: Array<{ source: string; destination: string; overwrite: boolean }> = []
    server.use(
      http.get('http://localhost/api/v1/files', () => HttpResponse.json({
        path: '/', advancedMode: false,
        entries: [
          { name: 'report.txt', path: '/report.txt', type: 'file', size: 12, modifiedAt: '2026-01-01T00:00:00Z' },
          { name: 'Archive', path: '/Archive', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
        ],
      })),
      http.post('http://localhost/api/v1/files/move', async ({ request }) => {
        moves.push(await request.json() as { source: string; destination: string; overwrite: boolean })
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    renderApp('/files')

    const file = await screen.findByRole('row', { name: /report\.txt/ })
    const folder = screen.getByRole('row', { name: /Archive/ })
    const transfer = { types: [], files: [], effectAllowed: 'none', dropEffect: 'none', setData: vi.fn() }
    fireEvent.dragStart(file, { dataTransfer: transfer })
    expect(file).toHaveAttribute('draggable', 'true')
    expect(folder).toHaveAttribute('draggable', 'true')
    fireEvent.dragOver(folder, { dataTransfer: transfer })
    expect(folder).toHaveClass('drop-target')
    fireEvent.drop(folder, { dataTransfer: transfer })

    const dialog = screen.getByRole('dialog', { name: 'Move' })
    expect(dialog).toHaveTextContent('Are you sure you want to move report.txt to Archive?')
    expect(within(dialog).getByText('report.txt')).toHaveStyle({ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace' })
    expect(within(dialog).getByText('Archive')).toHaveStyle({ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace' })
    expect(moves).toEqual([])
    await user.click(screen.getByRole('button', { name: 'Move' }))
    await waitFor(() => expect(moves).toEqual([{ source: '/report.txt', destination: '/Archive/report.txt', overwrite: false }]))
  })

  it('sorts list entries from their table headers', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [
        { name: 'Zulu', path: '/Zulu', type: 'directory', size: 1, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'Archive', path: '/Archive', type: 'directory', size: 8192, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'alpha.txt', path: '/alpha.txt', type: 'file', size: 100, modifiedAt: '2026-01-03T00:00:00Z' },
        { name: 'zebra.txt', path: '/zebra.txt', type: 'file', size: 10, modifiedAt: '2026-01-04T00:00:00Z' },
      ],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    await screen.findByRole('row', { name: /alpha\.txt/ })
    expect(screen.queryByRole('button', { name: 'Date created' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Date modified' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Size' }))
    expect(screen.getAllByRole('row').slice(1).map((row) => row.textContent)).toEqual([
      expect.stringContaining('Archive'),
      expect.stringContaining('Zulu'),
      expect.stringContaining('zebra.txt'),
      expect.stringContaining('alpha.txt'),
    ])
  })

  it('restores the saved file sort preference', async () => {
    localStorage.setItem('zenfm.files.sort', JSON.stringify({ sort: 'size', direction: 'desc' }))
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [
        { name: 'small.txt', path: '/small.txt', type: 'file', size: 10, modifiedAt: '2026-01-01T00:00:00Z' },
        { name: 'large.txt', path: '/large.txt', type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z' },
      ],
    })))
    renderApp('/files')

    await screen.findByRole('row', { name: /large\.txt/ })
    expect(screen.getAllByRole('row').slice(1).map((row) => row.textContent)).toEqual([
      expect.stringContaining('large.txt'),
      expect.stringContaining('small.txt'),
    ])
  })

  it('creates a new text file without overwriting and opens the editor', async () => {
    let csrf = ''
    let condition = ''
    let body = 'not-empty'
    server.use(
      http.put('*/api/v1/files/content', async ({ request }) => {
        csrf = request.headers.get('X-ZenFM-CSRF') ?? ''
        condition = request.headers.get('If-None-Match') ?? ''
        body = await request.text()
        return new HttpResponse(null, { status: 204 })
      }),
      http.get('http://localhost/api/v1/files/content', () => HttpResponse.text('')),
    )
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    await user.click(screen.getByRole('button', { name: 'New file' }))
    await user.type(screen.getByLabelText('File name'), 'notes.txt')
    await user.keyboard('{Enter}')

    expect(await screen.findByText('Editing notes.txt')).toBeInTheDocument()
    expect(csrf).toBe('a'.repeat(32))
    expect(condition).toBe('*')
    expect(body).toBe('')

    await waitFor(() => expect(document.querySelector('.cm-content')).toBeInTheDocument())
    const editor = document.querySelector<HTMLElement>('.cm-content')!
    await user.click(editor)
    await user.keyboard('first{Enter}second')
    await waitFor(() => expect(editor.querySelectorAll('.cm-line')).toHaveLength(2))
    expect(screen.getByText('Editing notes.txt')).toBeInTheDocument()
    expect(condition).toBe('*')
    expect(body).toBe('')
  })

  it('opens the fullscreen viewer with Enter from a text preview', async () => {
    server.use(
      http.get('http://localhost/api/v1/files', () => HttpResponse.json({
        path: '/', advancedMode: false,
        entries: [{ name: 'notes.txt', path: '/notes.txt', type: 'file', size: 13, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }],
      })),
      http.get('http://localhost/api/v1/files/preview', () => HttpResponse.text('Preview text')),
      http.get('http://localhost/api/v1/files/content', () => HttpResponse.text('Preview text')),
    )
    const user = userEvent.setup()
    render(<TestProviders initialPath="/files"><App /><RouterProbe /></TestProviders>)

    await user.dblClick(await screen.findByRole('row', { name: /notes\.txt/ }))
    await screen.findByText('Preview text')
    await user.keyboard('{Enter}')

    const dialog = screen.getByRole('dialog', { name: 'notes.txt' })
    expect(dialog).toHaveClass('MuiDialog-paperFullScreen')
    expect(within(dialog).getByRole('button', { name: 'Find in file' })).toBeInTheDocument()
    expect(screen.queryByText('Editing notes.txt')).not.toBeInTheDocument()
    expect(screen.getByTestId('route-location')).toHaveTextContent('/files?file=notes.txt')

    fireEvent.click(screen.getByRole('button', { name: 'Browser back', hidden: true }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'notes.txt' })).not.toBeInTheDocument())
    expect(screen.getByTestId('route-location')).toHaveTextContent('/files')
  })

  it('opens a manually entered file URL fullscreen over its parent folder', async () => {
    server.use(
      http.get('http://localhost/api/v1/files', ({ request }) => {
        const path = new URL(request.url).searchParams.get('path')
        return HttpResponse.json({
          path: '/Books', advancedMode: false,
          entries: path === '/Books' ? [{ name: 'chapter.txt', path: '/Books/chapter.txt', type: 'file', size: 13, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }] : [],
        })
      }),
      http.get('http://localhost/api/v1/files/preview', () => HttpResponse.text('Chapter text')),
    )

    renderApp('/files/Books?file=chapter.txt')

    expect(await screen.findByText('Chapter text')).toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'chapter.txt' })).toHaveClass('MuiDialog-paperFullScreen')
    expect(screen.getByRole('navigation', { name: 'Breadcrumb', hidden: true })).toHaveTextContent('Books')
  })

  it('applies replace all to the current conflict and every remaining upload', async () => {
    const requests: string[] = []
    server.use(http.put('*/api/v1/files/content', ({ request }) => {
      const path = new URL(request.url).searchParams.get('path') ?? ''
      const condition = request.headers.get('If-None-Match') ?? ''
      requests.push(`${path}:${condition}`)
      if (condition === '*') return HttpResponse.json({ title: 'Conflict', status: 409 }, { status: 409, headers: { 'Content-Type': 'application/problem+json' } })
      return new HttpResponse(null, { status: 204 })
    }))
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, [
      new File(['one'], 'one.txt', { type: 'text/plain' }),
      new File(['two'], 'two.txt', { type: 'text/plain' }),
    ])

    expect(await screen.findByRole('heading', { name: 'File already exists' })).toBeInTheDocument()
    expect(requests).toEqual(['/one.txt:*', '/two.txt:*'])
    expect(screen.getByRole('button', { name: 'Skip all' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Upload with new name' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Replace all' }))
    await waitFor(() => expect(requests).toEqual(['/one.txt:*', '/two.txt:*', '/one.txt:', '/two.txt:']))
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'File already exists' })).not.toBeInTheDocument())
  })

  it('skips every conflict after choosing skip all but keeps uploading new files', async () => {
    const requests: string[] = []
    server.use(http.put('*/api/v1/files/content', ({ request }) => {
      const path = new URL(request.url).searchParams.get('path') ?? ''
      requests.push(path)
      if (path !== '/new.txt') return HttpResponse.json({ title: 'Conflict', status: 409 }, { status: 409, headers: { 'Content-Type': 'application/problem+json' } })
      return new HttpResponse(null, { status: 201 })
    }))
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, [
      new File(['one'], 'one.txt'),
      new File(['two'], 'two.txt'),
      new File(['new'], 'new.txt'),
    ])

    await user.click(await screen.findByRole('button', { name: 'Skip all' }))
    await waitFor(() => expect(requests).toEqual(['/one.txt', '/two.txt', '/new.txt']))
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'File already exists' })).not.toBeInTheDocument())
  })

  it('cancels the rest of an upload batch from the conflict dialog', async () => {
    const requests: string[] = []
    server.use(http.put('*/api/v1/files/content', ({ request }) => {
      requests.push(new URL(request.url).searchParams.get('path') ?? '')
      return HttpResponse.json({ title: 'Conflict', status: 409 }, { status: 409, headers: { 'Content-Type': 'application/problem+json' } })
    }))
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, [new File(['one'], 'one.txt'), new File(['two'], 'two.txt')])
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('heading', { name: 'File already exists' })).not.toBeInTheDocument())
    expect(requests).toEqual(['/one.txt', '/two.txt'])
  })

  it('dismisses a pending conflict when another upload fails', async () => {
    vi.spyOn(api.files, 'uploadWithProgress').mockImplementation(async (path) => {
      if (path === '/conflict.txt') {
        const { ApiError } = await import('../api/client')
        throw new ApiError(409, { title: 'Conflict', status: 409 })
      }
      if (path === '/failed.txt') {
        await new Promise((resolve) => setTimeout(resolve, 20))
        throw new Error('storage failed')
      }
    })
    const user = userEvent.setup()
    renderApp('/files')
    await screen.findByText('Nothing here yet')

    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, [new File(['one'], 'conflict.txt'), new File(['two'], 'failed.txt')])

    expect(await screen.findByRole('heading', { name: 'File already exists' })).toBeInTheDocument()
    expect(await screen.findByText('storage failed')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'File already exists' })).not.toBeInTheDocument())
    expect(screen.queryByRole('progressbar', { name: 'Total upload progress' })).not.toBeInTheDocument()
  })

  it('shows lazy bounded image thumbnails in grid view', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [{ name: 'cover.tiff', path: '/cover.tiff', type: 'file', size: 512, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'image/tiff' }],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    await user.click(await screen.findByRole('button', { name: 'Grid view' }))
    const thumbnail = await screen.findByRole('img', { name: 'cover.tiff' })
    expect(thumbnail).toHaveAttribute('loading', 'lazy')
    expect(thumbnail.getAttribute('src')).toContain('/api/v1/files/preview?path=%2Fcover.tiff')
  })

  it('does not offer SVG files as raster thumbnails', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [{ name: 'vector.svg', path: '/vector.svg', type: 'file', size: 512, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'image/svg+xml' }],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    await user.click(await screen.findByRole('button', { name: 'Grid view' }))
    expect(await screen.findByText('vector.svg')).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: 'vector.svg' })).not.toBeInTheDocument()
  })

  it('does not present filesystem metadata as a folder size in grid view', async () => {
    server.use(http.get('http://localhost/api/v1/files', () => HttpResponse.json({
      path: '/', advancedMode: false,
      entries: [
        { name: 'Books', path: '/Books', type: 'directory', size: 4096, modifiedAt: '2026-01-01T12:00:00Z' },
        { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 512, modifiedAt: '2026-01-02T12:00:00Z' },
      ],
    })))
    const user = userEvent.setup()
    renderApp('/files')

    await user.click(await screen.findByRole('button', { name: 'Grid view' }))
    const folder = screen.getByRole('listitem', { name: 'Books' })
    const file = screen.getByRole('listitem', { name: 'notes.txt' })
    expect(within(folder).getByText(formatShortDate('2026-01-01T12:00:00Z'))).toBeInTheDocument()
    expect(folder).not.toHaveTextContent('4 KB')
    expect(within(file).getByText(`512 B · ${formatShortDate('2026-01-02T12:00:00Z')}`)).toBeInTheDocument()
    expect(getComputedStyle(within(folder).getByTestId('FolderRoundedIcon').parentElement!).alignItems).toBe('center')
  })

  it('archives highlighted regular entries without showing selection checkboxes', async () => {
    let archived: string[] = []
    server.use(
      http.get('http://localhost/api/v1/files', () => HttpResponse.json({
        path: '/', advancedMode: true,
        entries: [
          { name: 'Books', path: '/Books', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
          { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' },
          { name: 'socket', path: '/run/socket', type: 'special', size: 0, modifiedAt: '2026-01-01T00:00:00Z' },
        ],
      })),
      http.post('http://localhost/api/v1/files/archive-tickets', async ({ request }) => {
        archived = ((await request.json()) as { paths: string[] }).paths
        return HttpResponse.json({ url: '/api/v1/files/archive/zfm_archive_test' }, { status: 201 })
      }),
    )
    let downloadURL = ''
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { downloadURL = this.href })
    const user = userEvent.setup()
    renderApp('/files')

    const books = await screen.findByRole('row', { name: /Books/ })
    const notes = screen.getByRole('row', { name: /notes\.txt/ })
    fireEvent.click(books)
    fireEvent.click(notes, { ctrlKey: true })
    expect(books).toHaveClass('selected')
    expect(notes).toHaveClass('selected')
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    fireEvent.contextMenu(notes, { clientX: 200, clientY: 100 })
    await user.click(within(screen.getByRole('menu')).getByRole('menuitem', { name: 'Download 2 items' }))
    await waitFor(() => expect(archived).toEqual(['/Books', '/notes.txt']))
    await waitFor(() => expect(downloadURL).toContain('/api/v1/files/archive/'))
    expect(new URL(downloadURL).pathname).toBe('/api/v1/files/archive/zfm_archive_test')
    expect(downloadURL).not.toContain('blob:')
  })
})
