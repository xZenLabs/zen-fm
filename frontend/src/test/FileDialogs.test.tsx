import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider, createTheme } from '@mui/material'
import { http, HttpResponse } from 'msw'
import { canEdit, FileEditorDialog, FilePreviewDialog, PathActionDialog } from '../components/FileDialogs'
import TextEditor from '../components/TextEditor'
import { server } from './server'
import type { FileEntry } from '../api/types'
import { installModalNavigationGuard } from '../modalNavigation'

installModalNavigationGuard()

it('sanitizes an HTML preview before inserting it into the document', async () => {
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse('<p>Safe text</p><script>window.pwned=true</script><img src=x onerror="window.pwned=true">', { headers: { 'Content-Type': 'text/html' } })))
  const entry: FileEntry = { name: 'index.html', path: '/index.html', type: 'file', size: 80, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/html' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  expect(await screen.findByText('Safe text')).toBeInTheDocument()
  await waitFor(() => expect(document.querySelector('script')).not.toBeInTheDocument())
  expect(document.querySelector('.html-preview img')).not.toBeInTheDocument()
})

it('renders Markdown while escaping raw HTML and unsafe links', async () => {
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse('# Safe heading\n\n<script>window.pwned=true</script>\n\n[unsafe](javascript:window.pwned=true)', { headers: { 'Content-Type': 'text/plain' } })))
  const entry: FileEntry = { name: 'notes.md', path: '/notes.md', type: 'file', size: 120, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/markdown' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  expect(await screen.findByRole('heading', { name: 'Safe heading' })).toBeInTheDocument()
  expect(document.querySelector('.markdown-preview script')).not.toBeInTheDocument()
  const unsafe = document.querySelector('.markdown-preview a')
  expect(unsafe).toHaveTextContent('unsafe')
  expect(unsafe).not.toHaveAttribute('href')
})

it('renders CSV cells as inert text in a bounded table', async () => {
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse('name,value\nSafe,"<img src=x onerror=window.pwned=true>"', { headers: { 'Content-Type': 'text/plain' } })))
  const entry: FileEntry = { name: 'data.csv', path: '/data.csv', type: 'file', size: 70, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/csv' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  expect(await screen.findByRole('columnheader', { name: 'name' })).toBeInTheDocument()
  expect(screen.getByRole('cell', { name: '<img src=x onerror=window.pwned=true>' })).toBeInTheDocument()
  expect(document.querySelector('.csv-preview img')).not.toBeInTheDocument()
})

it('loads PDF from bounded preview into a revoked object URL', async () => {
  let credentials = ''
  server.use(http.get('http://localhost/api/v1/files/preview', ({ request }) => {
    credentials = request.credentials
    return new HttpResponse(new Uint8Array([37, 80, 68, 70]), { headers: { 'Content-Type': 'application/pdf' } })
  }))
  const createObjectURL = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:zenfm-preview')
  const revokeObjectURL = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
  const entry: FileEntry = { name: 'guide.pdf', path: '/guide.pdf', type: 'file', size: 4, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'application/pdf' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  const frame = await screen.findByTitle('guide.pdf')
  expect(frame).toHaveAttribute('src', 'blob:zenfm-preview')
  expect(frame).toHaveAttribute('sandbox', '')
  expect(frame).toHaveClass('preview-frame')
  expect(frame).not.toHaveAttribute('style')
  expect(credentials).toBe('same-origin')
  expect(createObjectURL).toHaveBeenCalledOnce()
  view.unmount()
  expect(revokeObjectURL).toHaveBeenCalledWith('blob:zenfm-preview')
})

it('opens EPUB preview in an empty sandbox without script or origin privileges', async () => {
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse('<h1>Chapter</h1>', { headers: { 'Content-Type': 'text/html' } })))
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:zenfm-epub')
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
  const entry: FileEntry = { name: 'book.epub', path: '/book.epub', type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'application/epub+zip' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  const frame = await screen.findByTitle('book.epub')
  expect(frame).toHaveAttribute('src', 'blob:zenfm-epub')
  expect(frame).toHaveAttribute('sandbox', '')
  expect(frame).not.toHaveAttribute('allow')
})

it.each([
  ['audio', { name: 'quiet.mp3', path: '/quiet.mp3', mimeType: 'audio/mpeg' }],
  ['video', { name: 'quiet.mp4', path: '/quiet.mp4', mimeType: 'video/mp4' }],
] as const)('routes %s playback through the bounded preview endpoint', (kind, partial) => {
  const entry: FileEntry = { ...partial, type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  const media = document.querySelector(kind)
  expect(media).not.toBeNull()
  expect(media).toHaveAttribute('src', expect.stringContaining('/api/v1/files/preview?path='))
  expect(media).not.toHaveAttribute('src', expect.stringContaining('/api/v1/files/raw'))
})

it('does not route SVG through the raster preview endpoint', () => {
  const entry: FileEntry = { name: 'vector.svg', path: '/vector.svg', type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'image/svg+xml' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  expect(screen.getByText('Preview is unavailable for this file.')).toBeInTheDocument()
  expect(document.querySelector('img')).not.toBeInTheDocument()
})

it('places preview close in the title bar and download in the footer', () => {
  const entry: FileEntry = { name: 'archive.bin', path: '/archive.bin', type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  const close = screen.getByRole('button', { name: 'Close' })
  const download = screen.getByRole('link', { name: 'Download' })
  expect(close.closest('.MuiDialogTitle-root')).toBeInTheDocument()
  expect(download.closest('.MuiDialogActions-root')).toBeInTheDocument()
})

it('shows a text preview with Open as the primary action and keeps Edit explicit', async () => {
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse('Readable text', { headers: { 'Content-Type': 'text/plain' } })))
  const entry: FileEntry = { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 13, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onEdit = vi.fn()
  render(<ThemeProvider theme={createTheme({ palette: { mode: 'light', background: { default: '#f5f5f1', paper: '#ffffff' } } })}><QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} onEdit={onEdit} /></QueryClientProvider></ThemeProvider>)

  const text = await screen.findByText('Readable text')
  const editor = text.closest('.cm-editor')
  const scroller = editor?.querySelector('.cm-scroller')
  expect(editor?.closest('.cm-theme-light')).toBeInTheDocument()
  expect(scroller?.querySelector('.cm-lineNumbers')).toBeInTheDocument()
  expect(scroller?.querySelector('.cm-content')).toHaveAttribute('contenteditable', 'false')
  const dialog = screen.getByRole('dialog', { name: 'notes.txt' })
  expect(getComputedStyle(dialog).backgroundColor).toBe('rgb(255, 255, 255)')
  expect(getComputedStyle(dialog.querySelector('.MuiDialogTitle-root')!).backgroundColor).toBe('rgb(255, 255, 255)')
  expect(getComputedStyle(dialog.querySelector('.MuiDialogContent-root')!).backgroundColor).toBe('rgb(255, 255, 255)')
  expect(getComputedStyle(dialog.querySelector('.MuiDialogActions-root')!).backgroundColor).toBe('rgb(255, 255, 255)')
  const edit = screen.getByRole('button', { name: 'Edit' })
  const open = screen.getByRole('button', { name: 'Open' })
  expect(within(edit).getByTestId('EditDocumentIcon')).toBeInTheDocument()
  expect(edit).not.toHaveClass('MuiButton-contained')
  expect(open).toHaveClass('MuiButton-contained')
  fireEvent.click(open)
  expect(dialog).toHaveClass('MuiDialog-paperFullScreen')
  fireEvent.click(edit)
  expect(onEdit).toHaveBeenCalledOnce()
})

it('finds and highlights text in the fullscreen viewer while keeping the search input focused', async () => {
  const source = 'Alpha beta alpha'
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse(source, { headers: { 'Content-Type': 'text/plain' } })))
  const entry: FileEntry = { name: 'notes.txt', path: '/notes.txt', type: 'file', size: source.length, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  await screen.findByText(source)
  await user.click(screen.getByRole('button', { name: 'Open' }))
  await waitFor(() => expect(document.querySelector('.cm-content')).toHaveTextContent(source))

  const closeFile = screen.getByRole('button', { name: 'Close' })
  await user.hover(closeFile)
  expect(await screen.findByRole('tooltip', { name: 'Close file' })).toBeInTheDocument()
  await user.unhover(closeFile)
  await waitFor(() => expect(screen.queryByRole('tooltip', { name: 'Close file' })).not.toBeInTheDocument())

  fireEvent.keyDown(document, { key: 'f', ctrlKey: true })
  const findInput = await screen.findByRole('textbox', { name: 'Find in file' })
  expect(findInput.closest('.file-find-control')).toHaveClass('open')
  expect(findInput.closest('.MuiDialogTitle-root')).toBeInTheDocument()
  await user.type(findInput, 'alpha')
  expect(findInput).toHaveFocus()
  expect(findInput).toHaveValue('alpha')
  expect(await screen.findByText('1 of 2')).toBeInTheDocument()
  await waitFor(() => expect(document.querySelectorAll('.cm-zen-find-match')).toHaveLength(2))
  expect(document.querySelector('.cm-zen-find-current')).toHaveTextContent('Alpha')

  const previous = screen.getByRole('button', { name: 'Previous match' })
  const next = screen.getByRole('button', { name: 'Next match' })
  expect(within(previous).getByTestId('KeyboardArrowLeftRoundedIcon')).toBeInTheDocument()
  expect(within(next).getByTestId('KeyboardArrowRightRoundedIcon')).toBeInTheDocument()
  await user.hover(previous)
  expect(await screen.findByRole('tooltip', { name: 'Previous match' })).toBeInTheDocument()
  await user.unhover(previous)
  await waitFor(() => expect(screen.queryByRole('tooltip', { name: 'Previous match' })).not.toBeInTheDocument())
  await user.hover(next)
  expect(await screen.findByRole('tooltip', { name: 'Next match' })).toBeInTheDocument()
  await user.unhover(next)
  await waitFor(() => expect(screen.queryByRole('tooltip', { name: 'Next match' })).not.toBeInTheDocument())

  const clear = screen.getByRole('button', { name: 'Clear find' })
  expect(clear.closest('.MuiInputAdornment-positionEnd')).toBeInTheDocument()
  await user.hover(clear)
  expect(await screen.findByRole('tooltip', { name: 'Clear find' })).toBeInTheDocument()
  await user.unhover(clear)
  await user.click(clear)
  expect(findInput).toHaveValue('')
  expect(findInput).toHaveFocus()
  await waitFor(() => expect(document.querySelector('.cm-zen-find-match')).not.toBeInTheDocument())

  await user.type(findInput, 'alpha')
  await user.click(next)
  expect(await screen.findByText('2 of 2')).toBeInTheDocument()
  await waitFor(() => expect(document.querySelector('.cm-zen-find-current')).toHaveTextContent('alpha'))
})

it('shows JSON source in the viewer when the server labels it application/json', async () => {
  const source = '{\n  "enabled": true\n}'
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse(source, { headers: { 'Content-Type': 'application/json' } })))
  const entry: FileEntry = { name: 'config.json', path: '/config.json', type: 'file', size: source.length, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'application/json' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  await waitFor(() => expect(document.querySelector('.cm-content')).toHaveTextContent('"enabled": true'))
  expect(document.querySelector('.cm-lineNumbers')).toBeInTheDocument()
})

it('uses a full-height dark CodeMirror theme in dark mode', () => {
  const view = render(<ThemeProvider theme={createTheme({ palette: { mode: 'dark', background: { default: '#0d1117', paper: '#161b22' } } })}><TextEditor name="notes.txt" value="Readable text" onChange={() => undefined} /></ThemeProvider>)

  const editor = view.container.querySelector('.cm-theme-dark')
  expect(editor).toBeInTheDocument()
  expect(editor).toHaveStyle({ height: '100%' })
  expect(editor?.querySelector('.cm-lineNumbers')).toBeInTheDocument()
  expect(getComputedStyle(editor!.querySelector('.cm-editor')!).backgroundColor).toBe('rgb(22, 27, 34)')
  expect(getComputedStyle(editor!.querySelector('.cm-gutters')!).backgroundColor).toBe('rgb(22, 27, 34)')
})

it('limits find decorations to the visible viewport for long text files', async () => {
  const lines = Array.from({ length: 5_000 }, (_, index) => `alpha line ${index}`).join('\n')
  const view = render(<TextEditor name="large.txt" value={lines} readOnly fullHeight find={{ query: 'alpha', current: { from: 0, to: 5 } }} />)

  await waitFor(() => expect(view.container.querySelector('.cm-zen-find-match')).toBeInTheDocument())
  expect(view.container.querySelectorAll('.cm-zen-find-match').length).toBeLessThan(5_000)
})

it('syntax-highlights Lua source by filename', () => {
  const view = render(<TextEditor name="main.lua" value={'local enabled = true\nreturn enabled'} onChange={() => undefined} />)

  expect(view.container.querySelectorAll('.cm-line span').length).toBeGreaterThan(0)
})

it('loads editable text through the bounded exact-source endpoint', async () => {
  let requestedContent = false
  server.use(
    http.get('http://localhost/api/v1/files/content', () => {
      requestedContent = true
      return new HttpResponse('bounded text', { headers: { 'Content-Type': 'text/plain' } })
    }),
    http.get('http://localhost/api/v1/files/raw', () => {
      throw new Error('the editor must not load unbounded raw content')
    }),
  )
  const entry: FileEntry = { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 12, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<ThemeProvider theme={createTheme({ palette: { mode: 'dark', background: { default: '#0d1117', paper: '#161b22' } } })}><QueryClientProvider client={client}><FileEditorDialog entry={entry} onClose={() => undefined} onSaved={() => undefined} /></QueryClientProvider></ThemeProvider>)

  await waitFor(() => expect(requestedContent).toBe(true))
  const dialog = screen.getByRole('dialog', { name: 'Editing notes.txt' })
  expect(dialog).toHaveClass('MuiDialog-paperFullScreen')
  expect(getComputedStyle(dialog).backgroundColor).toBe('rgb(13, 17, 23)')
  expect(getComputedStyle(dialog.querySelector('.MuiDialogTitle-root')!).backgroundColor).toBe('rgb(13, 17, 23)')
  expect(getComputedStyle(dialog.querySelector('.MuiDialogContent-root')!).backgroundColor).toBe('rgb(22, 27, 34)')
  expect(getComputedStyle(dialog.querySelector('.MuiDialogActions-root')!).backgroundColor).toBe('rgb(13, 17, 23)')
  expect(within(dialog).getByRole('button', { name: 'Close' }).closest('.MuiDialogTitle-root')).toBeInTheDocument()
  expect(within(dialog).queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
})

it('prompts to save unsaved edits when browser navigation requests the editor to close', async () => {
  let saved = ''
  server.use(
    http.get('http://localhost/api/v1/files/content', () => HttpResponse.text('Original text')),
    http.put('http://localhost/api/v1/files/content', async ({ request }) => {
      saved = await request.text()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  window.history.replaceState({ idx: 20 }, '', window.location.href)
  vi.spyOn(window.history, 'go').mockImplementation(() => undefined)
  const entry: FileEntry = { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 13, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onClose = vi.fn()
  const onSaved = vi.fn()
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><FileEditorDialog entry={entry} onClose={onClose} onSaved={onSaved} /></QueryClientProvider>)

  await waitFor(() => expect(document.querySelector('.cm-content')).toHaveTextContent('Original text'))
  const content = document.querySelector<HTMLElement>('.cm-content')!
  await user.click(content)
  await user.keyboard('{End} changed')
  await waitFor(() => expect(content).toHaveTextContent('Original text changed'))
  act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 19 } })) })

  expect(await screen.findByRole('dialog', { name: 'Save changes before closing?' })).toBeInTheDocument()
  expect(onClose).not.toHaveBeenCalled()
  act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 20 } })) })
  await user.click(screen.getByRole('button', { name: 'Save and close' }))

  await waitFor(() => expect(saved).toBe('Original text changed'))
  expect(onSaved).toHaveBeenCalledOnce()
  expect(onClose).toHaveBeenCalledOnce()
})

it('refuses to load oversized files into the text editor', () => {
  const entry: FileEntry = { name: 'huge.txt', path: '/huge.txt', type: 'file', size: 4 * 1024 * 1024 + 1, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  expect(canEdit(entry)).toBe(false)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FileEditorDialog entry={entry} onClose={() => undefined} onSaved={() => undefined} /></QueryClientProvider>)

  expect(screen.getByText('This file is too large or unsupported for safe editing.')).toBeInTheDocument()
})

it('keeps extensionless text files editable after their directory entry is refreshed', () => {
  const entry: FileEntry = { name: 'README', path: '/README', type: 'file', size: 12, modifiedAt: '2026-01-01T00:00:00Z' }
  expect(canEdit(entry)).toBe(true)
})

it('offers an explicit replacement after a copy destination conflict', async () => {
  const overwriteRequests: boolean[] = []
  server.use(
    http.post('http://localhost/api/v1/files/copy-size', () => HttpResponse.json({ items: [{ source: '/Downloads/zenfm.koplugin', bytes: 10 }], totalBytes: 10 })),
    http.post('http://localhost/api/v1/files/copy', async ({ request }) => {
      const body = await request.json() as { overwrite: boolean }
      overwriteRequests.push(body.overwrite)
      if (!body.overwrite) {
        return new HttpResponse('{"copiedBytes":10}\n{"copiedBytes":10,"error":{"title":"Conflict","status":409,"detail":"destination already exists"}}\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
      }
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const entry: FileEntry = { name: 'zenfm.koplugin', path: '/Downloads/zenfm.koplugin', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onClose = vi.fn()
  const onDone = vi.fn()
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><PathActionDialog action="copy" entries={[entry]} onClose={onClose} onDone={onDone} /></QueryClientProvider>)

  const destination = screen.getByLabelText('Destination path')
  await user.clear(destination)
  await user.type(destination, '/koreader/plugins/zenfm.koplugin')
  await user.click(screen.getByRole('button', { name: 'Confirm' }))

  expect(await screen.findByRole('button', { name: 'Replace' })).toBeInTheDocument()
  expect(overwriteRequests).toEqual([false])
  await user.click(screen.getByRole('button', { name: 'Replace' }))
  await waitFor(() => expect(overwriteRequests).toEqual([false, true]))
  expect(onDone).toHaveBeenCalledOnce()
  expect(onClose).toHaveBeenCalledOnce()
})

it('asks whether to overwrite or duplicate a same-folder paste', async () => {
  const requests: Array<{ source: string; destination: string; overwrite: boolean }> = []
  server.use(
    http.get('http://localhost/api/v1/files', ({ request }) => {
      expect(new URL(request.url).searchParams.get('path')).toBe('/Downloads')
      expect(new URL(request.url).searchParams.get('hidden')).toBe('true')
      return HttpResponse.json({
        path: '/Downloads', advancedMode: false,
        entries: [
          { name: 'notes.txt', path: '/Downloads/notes.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' },
          { name: 'notes copy.txt', path: '/Downloads/notes copy.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' },
        ],
      })
    }),
    http.post('http://localhost/api/v1/files/copy-size', () => HttpResponse.json({ items: [{ source: '/Downloads/notes.txt', bytes: 8 }], totalBytes: 8 })),
    http.post('http://localhost/api/v1/files/copy', async ({ request }) => {
      requests.push(await request.json() as { source: string; destination: string; overwrite: boolean })
      return new HttpResponse('{"copiedBytes":8,"done":true}\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
    }),
  )
  const entry: FileEntry = { name: 'notes.txt', path: '/Downloads/notes.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onClose = vi.fn()
  const onDone = vi.fn()
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><PathActionDialog action="copy" entries={[entry]} copyDestination="/Downloads" onClose={onClose} onDone={onDone} /></QueryClientProvider>)

  expect(screen.getByRole('heading', { name: 'File already exists' })).toBeInTheDocument()
  expect(screen.getByText('notes.txt already exists in this folder. Do you want to overwrite it or create a duplicate?')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Overwrite' })).toBeInTheDocument()
  expect(requests).toEqual([])

  await user.click(screen.getByRole('button', { name: 'Duplicate' }))

  await waitFor(() => expect(requests).toEqual([{ source: '/Downloads/notes.txt', destination: '/Downloads/notes copy 2.txt', overwrite: false }]))
  expect(onDone).toHaveBeenCalledOnce()
  expect(onClose).toHaveBeenCalledOnce()
})

it('explicitly overwrites when that same-folder paste choice is confirmed', async () => {
  const requests: Array<{ source: string; destination: string; overwrite: boolean }> = []
  server.use(
    http.post('http://localhost/api/v1/files/copy-size', () => HttpResponse.json({ items: [{ source: '/Downloads/notes.txt', bytes: 8 }], totalBytes: 8 })),
    http.post('http://localhost/api/v1/files/copy', async ({ request }) => {
      requests.push(await request.json() as { source: string; destination: string; overwrite: boolean })
      return new HttpResponse('{"copiedBytes":8,"done":true}\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
    }),
  )
  const entry: FileEntry = { name: 'notes.txt', path: '/Downloads/notes.txt', type: 'file', size: 8, modifiedAt: '2026-01-01T00:00:00Z' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><PathActionDialog action="copy" entries={[entry]} copyDestination="/Downloads" onClose={() => undefined} onDone={() => undefined} /></QueryClientProvider>)

  await user.click(screen.getByRole('button', { name: 'Overwrite' }))

  await waitFor(() => expect(requests).toEqual([{ source: '/Downloads/notes.txt', destination: '/Downloads/notes.txt', overwrite: true }]))
})

it('moves an entry into a folder chosen in the dialog', async () => {
  const requests: Array<{ source: string; destination: string; overwrite: boolean }> = []
  server.use(
    http.get('http://localhost/api/v1/files', ({ request }) => {
      const path = new URL(request.url).searchParams.get('path')
      if (path === '/Downloads') return HttpResponse.json({ path, advancedMode: false, entries: [{ name: 'KOReader', path: '/Downloads/KOReader', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }] })
      return HttpResponse.json({ path, advancedMode: false, entries: [] })
    }),
    http.post('http://localhost/api/v1/files/move', async ({ request }) => {
      requests.push(await request.json() as { source: string; destination: string; overwrite: boolean })
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const entry: FileEntry = { name: 'solitaire.koplugin', path: '/Downloads/solitaire.koplugin', type: 'directory', size: 0, modifiedAt: '2026-01-01T00:00:00Z' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onClose = vi.fn()
  const onDone = vi.fn()
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><PathActionDialog action="move" entries={[entry]} onClose={onClose} onDone={onDone} /></QueryClientProvider>)

  expect(await screen.findByText('/Downloads')).toBeInTheDocument()
  await user.click(await screen.findByRole('button', { name: 'KOReader' }))
  expect(await screen.findByText('/Downloads/KOReader')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Confirm' }))

  await waitFor(() => expect(requests).toEqual([{ source: '/Downloads/solitaire.koplugin', destination: '/Downloads/KOReader/solitaire.koplugin', overwrite: false }]))
  expect(onDone).toHaveBeenCalledOnce()
  expect(onClose).toHaveBeenCalledOnce()
})

it('shows aggregate copy progress and an ETA across multiple files', async () => {
  const encoder = new TextEncoder()
  let finishSecond: (() => void) | undefined
  let now = 1_000
  const clock = vi.spyOn(Date, 'now').mockImplementation(() => now)
  server.use(
    http.post('http://localhost/api/v1/files/copy-size', () => HttpResponse.json({
      items: [{ source: '/alpha.bin', bytes: 100 }, { source: '/bravo.bin', bytes: 100 }],
      totalBytes: 200,
    })),
    http.post('http://localhost/api/v1/files/copy', async ({ request }) => {
      const body = await request.json() as { source: string }
      if (body.source === '/alpha.bin') {
        return new HttpResponse('{"copiedBytes":100,"done":true}\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
      }
      now = 2_000
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(encoder.encode('{"copiedBytes":50}\n'))
          finishSecond = () => {
            controller.enqueue(encoder.encode('{"copiedBytes":100,"done":true}\n'))
            controller.close()
          }
        },
      })
      return new HttpResponse(stream, { headers: { 'Content-Type': 'application/x-ndjson' } })
    }),
  )
  const entries: FileEntry[] = [
    { name: 'alpha.bin', path: '/alpha.bin', type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z' },
    { name: 'bravo.bin', path: '/bravo.bin', type: 'file', size: 100, modifiedAt: '2026-01-01T00:00:00Z' },
  ]
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onClose = vi.fn()
  const onDone = vi.fn()
  const user = userEvent.setup()
  render(<QueryClientProvider client={client}><PathActionDialog action="copy" entries={entries} onClose={onClose} onDone={onDone} /></QueryClientProvider>)

  await user.click(screen.getByRole('button', { name: 'Confirm' }))

  expect(await screen.findByText('Copying 150 B of 200 B — 75%')).toBeInTheDocument()
  expect(screen.getByText('About 1 second remaining')).toBeInTheDocument()
  expect(screen.getByRole('progressbar', { name: 'Total copy progress' })).toHaveAttribute('aria-valuenow', '75')
  finishSecond?.()
  await waitFor(() => expect(onDone).toHaveBeenCalledOnce())
  expect(onClose).toHaveBeenCalledOnce()
  clock.mockRestore()
})
