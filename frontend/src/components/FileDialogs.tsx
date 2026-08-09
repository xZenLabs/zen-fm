import { lazy, Suspense, useEffect, useMemo, useState, type FormEvent } from 'react'
import DOMPurify from 'dompurify'
import {
  Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, LinearProgress,
  MenuItem, Stack, TextField, Typography,
} from '@mui/material'
import CloseRounded from '@mui/icons-material/CloseRounded'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import type { CreateShareInput, FileEntry } from '../api/types'
import { ErrorPane, LoadingPane } from './Feedback'
import { renderMarkdown } from '../markdown'
import { parseCsv } from '../csv'

const TextEditor = lazy(() => import('./TextEditor'))

const MAX_EDITABLE_TEXT_BYTES = 4 * 1024 * 1024
const textExtensions = new Set(['txt', 'md', 'markdown', 'json', 'yaml', 'yml', 'toml', 'ini', 'log', 'csv', 'xml', 'html', 'css', 'js', 'ts', 'tsx', 'jsx', 'lua', 'go', 'sh'])
const rasterExtensions = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'tif', 'tiff'])

function extension(path: string) {
  return path.split('.').pop()?.toLowerCase() ?? ''
}

function isTextEntry(entry: FileEntry) {
  return entry.type === 'file' && (entry.mimeType?.startsWith('text/') || textExtensions.has(extension(entry.name)))
}

export function canEdit(entry: FileEntry) {
  return isTextEntry(entry) && entry.size <= MAX_EDITABLE_TEXT_BYTES
}

export function FilePreviewDialog({ entry, onClose }: { entry: FileEntry | null; onClose: () => void }) {
  const { t } = useTranslation()
  const ext = extension(entry?.name ?? '')
  const mime = entry?.mimeType ?? ''
  const needsText = Boolean(entry && (isTextEntry(entry) || ['html', 'htm', 'xhtml'].includes(ext)))
  const needsBlob = Boolean(entry && (mime === 'application/pdf' || ['pdf', 'epub', 'tif', 'tiff'].includes(ext)))
  const text = useQuery({
    queryKey: ['file-text', entry?.path],
    queryFn: () => api.files.readPreviewText(entry!.path),
    enabled: needsText,
  })
  const blob = useQuery({
    queryKey: ['file-preview-blob', entry?.path],
    queryFn: () => api.files.readPreviewBlob(entry!.path),
    enabled: needsBlob,
  })
  const [objectUrl, setObjectUrl] = useState('')
  useEffect(() => {
    if (!blob.data) {
      setObjectUrl('')
      return
    }
    const next = URL.createObjectURL(blob.data)
    setObjectUrl(next)
    return () => URL.revokeObjectURL(next)
  }, [blob.data])
  const raw = entry ? api.files.rawUrl(entry.path) : ''
  const previewUrl = entry ? api.files.previewUrl(entry.path) : ''
  const csv = useMemo(() => ext === 'csv' && text.data ? parseCsv(text.data) : null, [ext, text.data])

  let preview = <Typography color="text.secondary">{t('files.noPreview')}</Typography>
  if (['tif', 'tiff'].includes(ext)) {
    if (blob.isPending || !objectUrl) preview = <LoadingPane />
    else if (blob.error) preview = <ErrorPane error={blob.error} />
    else preview = <img src={objectUrl} alt={entry?.name} className="preview-media" />
  } else if (rasterExtensions.has(ext)) {
    preview = <img src={previewUrl} alt={entry?.name} className="preview-media" />
  } else if (mime.startsWith('audio/') || ['mp3', 'm4a', 'ogg', 'wav', 'flac'].includes(ext)) {
    preview = <audio src={previewUrl} controls className="preview-media" />
  } else if (mime.startsWith('video/') || ['mp4', 'webm', 'mkv', 'mov'].includes(ext)) {
    preview = <video src={previewUrl} controls className="preview-media" />
  } else if (mime === 'application/pdf' || ext === 'pdf') {
    if (blob.isPending || !objectUrl) preview = <LoadingPane />
    else if (blob.error) preview = <ErrorPane error={blob.error} />
    else preview = <iframe src={objectUrl} title={entry?.name} sandbox="" referrerPolicy="no-referrer" className="preview-frame" />
  } else if (ext === 'epub') {
    if (blob.isPending || !objectUrl) preview = <LoadingPane />
    else if (blob.error) preview = <ErrorPane error={blob.error} />
    else preview = <iframe src={objectUrl} title={entry?.name} sandbox="" referrerPolicy="no-referrer" className="preview-frame" />
  } else if (needsText) {
    if (text.isPending) preview = <LoadingPane />
    else if (text.error) preview = <ErrorPane error={text.error} />
    else if (['html', 'htm'].includes(ext)) preview = <Box className="html-preview" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(text.data ?? '', { FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'img', 'link', 'meta', 'base'], FORBID_ATTR: ['style'] }) }} />
    else if (['md', 'markdown'].includes(ext)) preview = <Box className="markdown-preview" dangerouslySetInnerHTML={{ __html: renderMarkdown(text.data ?? '') }} />
    else if (ext === 'csv' && csv) preview = <Box className="csv-preview" sx={{ overflow: 'auto' }}><table><thead>{csv.rows[0] && <tr>{csv.rows[0].map((cell, index) => <th key={index}>{cell}</th>)}</tr>}</thead><tbody>{csv.rows.slice(1).map((row, rowIndex) => <tr key={rowIndex}>{row.map((cell, cellIndex) => <td key={cellIndex}>{cell}</td>)}</tr>)}</tbody></table>{csv.truncated && <Typography variant="caption" color="text.secondary">Preview truncated.</Typography>}</Box>
    else preview = <Box component="pre" sx={{ p: 2, m: 0, overflow: 'auto', fontSize: '.86rem', whiteSpace: 'pre-wrap', bgcolor: 'action.hover', borderRadius: 1 }}>{text.data}</Box>
  }

  return (
    <Dialog open={Boolean(entry)} onClose={onClose} maxWidth="lg">
      <DialogTitle sx={{ display: 'flex', alignItems: 'center', gap: 1 }}><Box component="span" className="file-name" flex={1} minWidth={0}>{entry?.name}</Box><IconButton aria-label={t('common.close')} onClick={onClose}><CloseRounded /></IconButton></DialogTitle>
      <DialogContent dividers sx={{ minHeight: 240 }}>{preview}</DialogContent>
      <DialogActions>{entry && <Button component="a" href={raw} download>{t('files.download')}</Button>}</DialogActions>
    </Dialog>
  )
}

export function FileEditorDialog({ entry, onClose, onSaved }: { entry: FileEntry | null; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [value, setValue] = useState('')
  const editable = Boolean(entry && canEdit(entry))
  const file = useQuery({ queryKey: ['file-edit', entry?.path], queryFn: () => api.files.readText(entry!.path), enabled: editable })
  useEffect(() => { if (file.data !== undefined) setValue(file.data) }, [file.data])
  const save = useMutation({
    mutationFn: () => api.files.saveText(entry!.path, value),
    onSuccess: () => {
      queryClient.setQueryData(['file-text', entry?.path], value)
      void queryClient.invalidateQueries({ queryKey: ['file-preview-blob', entry?.path] })
      onSaved(); onClose()
    },
  })

  return (
    <Dialog open={Boolean(entry)} onClose={save.isPending ? undefined : onClose} fullScreen>
      <DialogTitle sx={{ display: 'flex', alignItems: 'center', gap: 1 }}><Box component="span" className="file-name" flex={1} minWidth={0}>{t('files.editor')} · {entry?.name}</Box><IconButton aria-label={t('common.close')} onClick={onClose} disabled={save.isPending}><CloseRounded /></IconButton></DialogTitle>
      <DialogContent dividers sx={{ p: 0, minHeight: 0, flex: 1 }}>
        {entry && !editable ? <Box p={2}><Typography color="text.secondary">{t('files.editorUnavailable')}</Typography></Box> : file.isPending ? <LoadingPane /> : file.error ? <Box p={2}><ErrorPane error={file.error} /></Box> : (
          <Suspense fallback={<LoadingPane />}><TextEditor name={entry?.name ?? ''} value={value} onChange={setValue} /></Suspense>
        )}
      </DialogContent>
      {save.error && <Box px={3} pt={1}><ErrorPane error={save.error} /></Box>}
      <DialogActions>
        <Button onClick={() => save.mutate()} variant="contained" disabled={!editable || save.isPending || file.isPending}>{t('files.save')}</Button>
      </DialogActions>
    </Dialog>
  )
}

type PathAction = 'rename' | 'move' | 'copy'

export function PathActionDialog({ action, entry, onClose, onDone }: { action: PathAction | null; entry: FileEntry | null; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [destination, setDestination] = useState('')
  useEffect(() => {
    if (!entry || !action) return
    const parent = entry.path.slice(0, Math.max(0, entry.path.lastIndexOf('/'))) || '/'
    setDestination(action === 'rename' ? entry.name : `${parent === '/' ? '' : parent}/${entry.name}`)
  }, [action, entry])

  const mutate = useMutation({
    mutationFn: async () => {
      if (!entry || !action) return
      const target = action === 'rename'
        ? `${entry.path.slice(0, Math.max(0, entry.path.lastIndexOf('/')))}/${destination}`.replaceAll('//', '/')
        : destination
      if (action === 'copy') await api.files.copy(entry.path, target)
      else await api.files.move(entry.path, target)
    },
    onSuccess: () => { onDone(); onClose() },
  })

  const submit = (event: FormEvent) => { event.preventDefault(); mutate.mutate() }
  return (
    <Dialog open={Boolean(action && entry)} onClose={mutate.isPending ? undefined : onClose} maxWidth="xs">
      <Stack component="form" onSubmit={submit}>
        <DialogTitle>{action ? t(`files.${action}`) : ''}</DialogTitle>
        <DialogContent>
          <TextField fullWidth autoFocus label={action === 'rename' ? t('files.name') : t('files.destination')} value={destination} onChange={(event) => setDestination(event.target.value)} error={Boolean(mutate.error)} helperText={mutate.error instanceof Error ? mutate.error.message : ''} />
        </DialogContent>
        <DialogActions><Button onClick={onClose}>{t('common.cancel')}</Button><Button type="submit" variant="contained" disabled={!destination || mutate.isPending}>{t('common.confirm')}</Button></DialogActions>
      </Stack>
    </Dialog>
  )
}

export function CreateShareDialog({ entry, onClose, onCreated }: { entry: FileEntry | null; onClose: () => void; onCreated: (url?: string) => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [expiresInSeconds, setExpiry] = useState<number | undefined>(86_400)
  const create = useMutation({
    mutationFn: () => api.shares.create({ path: entry!.path, name: name || undefined, password: password || undefined, expiresInSeconds } satisfies CreateShareInput),
    onSuccess: (share) => { onCreated(share.url); onClose() },
  })
  return (
    <Dialog open={Boolean(entry)} onClose={create.isPending ? undefined : onClose} maxWidth="xs">
      <DialogTitle>{t('shares.create')}</DialogTitle>
      <DialogContent><Stack gap={2} pt={0.5}>
        <Typography color="text.secondary" className="file-name">{entry?.path}</Typography>
        <TextField label={t('shares.name')} value={name} onChange={(event) => setName(event.target.value)} />
        <TextField label={t('shares.password')} type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
        <TextField select label={t('shares.expiry')} value={expiresInSeconds ?? 0} onChange={(event) => setExpiry(Number(event.target.value) || undefined)}>
          <MenuItem value={3600}>{t('shares.oneHour')}</MenuItem><MenuItem value={86400}>{t('shares.oneDay')}</MenuItem><MenuItem value={604800}>{t('shares.oneWeek')}</MenuItem><MenuItem value={0}>{t('shares.never')}</MenuItem>
        </TextField>
        {create.isPending && <LinearProgress />}
        {create.error && <ErrorPane error={create.error} />}
      </Stack></DialogContent>
      <DialogActions><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="contained" onClick={() => create.mutate()}>{t('common.create')}</Button></DialogActions>
    </Dialog>
  )
}
