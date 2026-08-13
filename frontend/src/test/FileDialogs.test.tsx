import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider, createTheme } from '@mui/material'
import { http, HttpResponse } from 'msw'
import { canEdit, FileEditorDialog, FilePreviewDialog, PathActionDialog } from '../components/FileDialogs'
import TextEditor from '../components/TextEditor'
import { server } from './server'
import type { FileEntry } from '../api/types'

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

it('uses a dark editor-like surface for opened text files', async () => {
  server.use(http.get('http://localhost/api/v1/files/preview', () => new HttpResponse('Readable text', { headers: { 'Content-Type': 'text/plain' } })))
  const entry: FileEntry = { name: 'notes.txt', path: '/notes.txt', type: 'file', size: 13, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FilePreviewDialog entry={entry} onClose={() => undefined} /></QueryClientProvider>)

  const text = await screen.findByText('Readable text')
  expect(text).toHaveStyle({ backgroundColor: '#010409', color: '#e6edf3' })
  expect(screen.getByRole('dialog', { name: 'notes.txt' })).toHaveStyle({ backgroundColor: '#0d1117' })
})

it('uses a full-height dark CodeMirror theme in dark mode', () => {
  const view = render(<ThemeProvider theme={createTheme({ palette: { mode: 'dark' } })}><TextEditor name="notes.txt" value="Readable text" onChange={() => undefined} /></ThemeProvider>)

  const editor = view.container.querySelector('.cm-theme-dark')
  expect(editor).toBeInTheDocument()
  expect(editor).toHaveStyle({ height: '100%' })
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
  render(<QueryClientProvider client={client}><FileEditorDialog entry={entry} onClose={() => undefined} onSaved={() => undefined} /></QueryClientProvider>)

  await waitFor(() => expect(requestedContent).toBe(true))
  const dialog = screen.getByRole('dialog', { name: 'Text editor · notes.txt' })
  expect(dialog).toHaveClass('MuiDialog-paperFullScreen')
  expect(within(dialog).getByRole('button', { name: 'Close' }).closest('.MuiDialogTitle-root')).toBeInTheDocument()
  expect(within(dialog).queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
})

it('refuses to load oversized files into the text editor', () => {
  const entry: FileEntry = { name: 'huge.txt', path: '/huge.txt', type: 'file', size: 4 * 1024 * 1024 + 1, modifiedAt: '2026-01-01T00:00:00Z', mimeType: 'text/plain' }
  expect(canEdit(entry)).toBe(false)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><FileEditorDialog entry={entry} onClose={() => undefined} onSaved={() => undefined} /></QueryClientProvider>)

  expect(screen.getByText('This file is too large or unsupported for safe editing.')).toBeInTheDocument()
})

it('offers an explicit replacement after a copy destination conflict', async () => {
  const overwriteRequests: boolean[] = []
  server.use(
    http.post('http://localhost/api/v1/files/copy-size', () => HttpResponse.json({ items: [{ source: '/Downloads/zenfm.koplugin', bytes: 10 }], totalBytes: 10 })),
    http.post('http://localhost/api/v1/files/copy', async ({ request }) => {
      const body = await request.json() as { overwrite: boolean }
      overwriteRequests.push(body.overwrite)
      if (!body.overwrite) {
        return HttpResponse.json({ title: 'Conflict', status: 409, detail: 'destination already exists' }, { status: 409, headers: { 'Content-Type': 'application/problem+json' } })
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
