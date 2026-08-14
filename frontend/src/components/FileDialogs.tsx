import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import DOMPurify from 'dompurify'
import {
  Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, InputAdornment, LinearProgress,
  MenuItem, Stack, TextField, Tooltip, Typography, useTheme,
} from '@mui/material'
import CloseRounded from '@mui/icons-material/CloseRounded'
import OpenInNewIcon from '@mui/icons-material/OpenInNew'
import KeyboardArrowLeftRounded from '@mui/icons-material/KeyboardArrowLeftRounded'
import KeyboardArrowRightRounded from '@mui/icons-material/KeyboardArrowRightRounded'
import SearchRounded from '@mui/icons-material/SearchRounded'
import DownloadIcon from '@mui/icons-material/Download'
import EditDocumentIcon from '@mui/icons-material/EditDocument'
import FolderRounded from '@mui/icons-material/FolderRounded'
import ArrowBackRounded from '@mui/icons-material/ArrowBackRounded'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, isConflictError } from '../api/client'
import type { CreateShareInput, FileEntry } from '../api/types'
import { ErrorPane, LoadingPane } from './Feedback'
import { PasswordField } from './PasswordField'
import { renderMarkdown } from '../markdown'
import { parseCsv } from '../csv'
import { formatBytes, formatDuration, joinPath } from '../utils'
import { useCloseOnHistoryNavigation } from '../modalNavigation'

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

function summarizeTextMatches(text: string, query: string) {
  if (!query) return { count: 0, first: -1, last: -1 }
  let count = 0
  let first = -1
  let last = -1
  for (let match = text.indexOf(query); match !== -1; match = text.indexOf(query, match + query.length)) {
    if (first === -1) first = match
    last = match
    count += 1
  }
  return { count, first, last }
}

function previousTextMatch(text: string, query: string, before: number) {
  let previous = -1
  for (let match = text.indexOf(query); match !== -1 && match < before; match = text.indexOf(query, match + query.length)) {
    previous = match
  }
  return previous
}

export function FilePreviewDialog({ entry, onClose, onEdit, fullScreen: fullScreenProp, onFullScreen }: { entry: FileEntry | null; onClose: () => void; onEdit?: () => void; fullScreen?: boolean; onFullScreen?: () => void }) {
  const { t } = useTranslation()
  const theme = useTheme()
  const surface = theme.palette.mode === 'dark' ? theme.palette.background.default : theme.palette.background.paper
  const contentSurface = theme.palette.background.paper
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
  const findButtonRef = useRef<HTMLButtonElement>(null)
  const findInputRef = useRef<HTMLInputElement>(null)
  const [localFullScreen, setLocalFullScreen] = useState(false)
  const fullScreen = fullScreenProp ?? localFullScreen
  const [findOpen, setFindOpen] = useState(false)
  const [findQuery, setFindQuery] = useState('')
  const [findIndex, setFindIndex] = useState(-1)
  const [findStart, setFindStart] = useState(-1)
  const searchableText = useMemo(() => (text.data ?? '').toLocaleLowerCase(), [text.data])
  const activeFindQuery = findOpen ? findQuery : ''
  const normalizedFindQuery = activeFindQuery.toLocaleLowerCase()
  const findSummary = useMemo(
    () => summarizeTextMatches(searchableText, normalizedFindQuery),
    [normalizedFindQuery, searchableText],
  )
  const currentFindMatch = activeFindQuery && findStart >= 0
    ? { from: findStart, to: findStart + activeFindQuery.length }
    : undefined
  useCloseOnHistoryNavigation(Boolean(entry) && fullScreenProp !== true, onClose)

  const showFind = useCallback(() => {
    setFindOpen(true)
    window.requestAnimationFrame(() => findInputRef.current?.select())
  }, [])

  useEffect(() => {
    setLocalFullScreen(false)
    setFindOpen(false)
    setFindQuery('')
  }, [entry?.path])

  useEffect(() => {
    if (!fullScreen || !needsText) return
    const handleFindShortcut = (event: globalThis.KeyboardEvent) => {
      if (event.key.toLocaleLowerCase() !== 'f' || (!event.ctrlKey && !event.metaKey)) return
      event.preventDefault()
      showFind()
    }
    document.addEventListener('keydown', handleFindShortcut)
    return () => document.removeEventListener('keydown', handleFindShortcut)
  }, [fullScreen, needsText, showFind])

  useEffect(() => {
    setFindIndex(findSummary.count > 0 ? 0 : -1)
    setFindStart(findSummary.first)
  }, [findSummary.count, findSummary.first, normalizedFindQuery, searchableText])

  const moveFind = (direction: number) => {
    if (findSummary.count === 0 || !normalizedFindQuery) return
    if (direction > 0) {
      const next = searchableText.indexOf(normalizedFindQuery, findStart + normalizedFindQuery.length)
      setFindStart(next === -1 ? findSummary.first : next)
    } else {
      const previous = previousTextMatch(searchableText, normalizedFindQuery, findStart)
      setFindStart(previous === -1 ? findSummary.last : previous)
    }
    setFindIndex((current) => (current + direction + findSummary.count) % findSummary.count)
  }
  const handleFindKey = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      event.stopPropagation()
      moveFind(event.shiftKey ? -1 : 1)
    } else if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      setFindOpen(false)
      window.requestAnimationFrame(() => findButtonRef.current?.focus())
    }
  }

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
    else if (fullScreen) preview = <Suspense fallback={<LoadingPane />}><TextEditor name={entry?.name ?? ''} value={text.data ?? ''} readOnly fullHeight find={{ query: activeFindQuery, current: currentFindMatch }} /></Suspense>
    else if (['html', 'htm'].includes(ext)) preview = <Box className="html-preview" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(text.data ?? '', { FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'img', 'link', 'meta', 'base'], FORBID_ATTR: ['style'] }) }} />
    else if (['md', 'markdown'].includes(ext)) preview = <Box className="markdown-preview" dangerouslySetInnerHTML={{ __html: renderMarkdown(text.data ?? '') }} />
    else if (ext === 'csv' && csv) preview = <Box className="csv-preview" sx={{ overflow: 'auto' }}><table><thead>{csv.rows[0] && <tr>{csv.rows[0].map((cell, index) => <th key={index}>{cell}</th>)}</tr>}</thead><tbody>{csv.rows.slice(1).map((row, rowIndex) => <tr key={rowIndex}>{row.map((cell, cellIndex) => <td key={cellIndex}>{cell}</td>)}</tr>)}</tbody></table>{csv.truncated && <Typography variant="caption" color="text.secondary">Preview truncated.</Typography>}</Box>
    else preview = <Suspense fallback={<LoadingPane />}><TextEditor name={entry?.name ?? ''} value={text.data ?? ''} readOnly /></Suspense>
  }

  return (
    <Dialog open={Boolean(entry)} onClose={onClose} maxWidth="lg" fullWidth fullScreen={fullScreen} slotProps={{ paper: { style: { backgroundColor: surface } } }}>
      <DialogTitle style={{ backgroundColor: surface }} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Box component="span" className="file-name" flex={1} minWidth={0}>{entry?.name}</Box>
        {fullScreen && needsText && <Box className={`file-find-control${findOpen ? ' open' : ''}`} role="search">
          <TextField
            fullWidth
            inputRef={findInputRef}
            placeholder={t('files.find')}
            value={findQuery}
            onChange={(event) => setFindQuery(event.target.value)}
            onKeyDown={handleFindKey}
            inputProps={{ 'aria-label': t('files.find'), tabIndex: findOpen ? 0 : -1 }}
            InputProps={{
              startAdornment: <InputAdornment position="start"><Tooltip title={t('files.find')}><IconButton ref={findButtonRef} aria-label={t('files.find')} aria-expanded={findOpen} onClick={showFind}><SearchRounded /></IconButton></Tooltip></InputAdornment>,
              endAdornment: findOpen ? <InputAdornment position="end">
                {findQuery && <Typography aria-live="polite" color="text.secondary" sx={{ whiteSpace: 'nowrap' }}>{findSummary.count > 0 ? t('files.findMatchCount', { current: findIndex + 1, total: findSummary.count }) : t('files.noFindMatches')}</Typography>}
                <Tooltip title={t('files.findPrevious')}><span><IconButton size="small" aria-label={t('files.findPrevious')} disabled={findSummary.count === 0} onClick={() => moveFind(-1)} sx={{ width: 32, height: 32, minWidth: 32, minHeight: 32 }}><KeyboardArrowLeftRounded /></IconButton></span></Tooltip>
                <Tooltip title={t('files.findNext')}><span><IconButton size="small" aria-label={t('files.findNext')} disabled={findSummary.count === 0} onClick={() => moveFind(1)} sx={{ width: 32, height: 32, minWidth: 32, minHeight: 32 }}><KeyboardArrowRightRounded /></IconButton></span></Tooltip>
                {findQuery && <Tooltip title={t('files.clearFind')}><IconButton edge="end" size="small" aria-label={t('files.clearFind')} onClick={() => { setFindQuery(''); findInputRef.current?.focus() }} sx={{ width: 32, height: 32, minWidth: 32, minHeight: 32 }}><CloseRounded /></IconButton></Tooltip>}
              </InputAdornment> : undefined,
            }}
          />
        </Box>}
        <Tooltip title={t('files.closeFile')}><IconButton aria-label={t('common.close')} onClick={onClose}><CloseRounded /></IconButton></Tooltip>
      </DialogTitle>
      <DialogContent dividers style={{ backgroundColor: contentSurface }} className={fullScreen ? 'file-viewer-content fullscreen-viewer' : undefined} sx={{ minHeight: fullScreen && needsText ? 0 : 240, p: fullScreen && needsText ? 0 : undefined, flex: fullScreen && needsText ? 1 : undefined }}>{preview}</DialogContent>
      <DialogActions style={{ backgroundColor: surface }}>
        {entry && <Button component="a" href={raw} download startIcon={<DownloadIcon />}>{t('files.download')}</Button>}
        {entry && onEdit && canEdit(entry) && <Button startIcon={<EditDocumentIcon />} onClick={onEdit}>{t('files.edit')}</Button>}
        {entry && !fullScreen && <Button variant="contained" startIcon={<OpenInNewIcon />} onClick={() => onFullScreen ? onFullScreen() : setLocalFullScreen(true)}>{t('files.preview')}</Button>}
      </DialogActions>
    </Dialog>
  )
}

export function FileEditorDialog({ entry, onClose, onSaved }: { entry: FileEntry | null; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const theme = useTheme()
  const surface = theme.palette.mode === 'dark' ? theme.palette.background.default : theme.palette.background.paper
  const contentSurface = theme.palette.background.paper
  const queryClient = useQueryClient()
  const [value, setValue] = useState('')
  const [confirmClose, setConfirmClose] = useState(false)
  const editable = Boolean(entry && canEdit(entry))
  const file = useQuery({ queryKey: ['file-edit', entry?.path], queryFn: () => api.files.readText(entry!.path), enabled: editable })
  useEffect(() => { if (entry && file.data !== undefined) setValue(file.data) }, [entry, file.data])
  const save = useMutation({
    mutationFn: () => api.files.saveText(entry!.path, value),
    onSuccess: () => {
      queryClient.setQueryData(['file-text', entry?.path], value)
      queryClient.setQueryData(['file-edit', entry?.path], value)
      void queryClient.invalidateQueries({ queryKey: ['file-preview-blob', entry?.path] })
      setConfirmClose(false)
      onSaved(); onClose()
    },
  })
  const dirty = editable && file.data !== undefined && value !== file.data
  const requestClose = () => {
    if (save.isPending) return
    if (dirty) setConfirmClose(true)
    else onClose()
  }
  const discardAndClose = () => {
    setValue(file.data ?? '')
    setConfirmClose(false)
    onClose()
  }
  useCloseOnHistoryNavigation(Boolean(entry), requestClose)
  useCloseOnHistoryNavigation(confirmClose, () => setConfirmClose(false))

  return (
    <>
    <Dialog open={Boolean(entry)} onClose={save.isPending ? undefined : requestClose} fullScreen slotProps={{ paper: { style: { backgroundColor: surface } } }}>
      <DialogTitle style={{ backgroundColor: surface }} sx={{ display: 'flex', alignItems: 'center', gap: 1 }}><Box component="span" className="file-name" flex={1} minWidth={0}>{t('files.editing', { name: entry?.name })}</Box><IconButton aria-label={t('common.close')} onClick={requestClose} disabled={save.isPending}><CloseRounded /></IconButton></DialogTitle>
      <DialogContent dividers style={{ backgroundColor: contentSurface }} sx={{ p: 0, minHeight: 0, flex: 1 }}>
        {entry && !editable ? <Box p={2}><Typography color="text.secondary">{t('files.editorUnavailable')}</Typography></Box> : file.isPending ? <LoadingPane /> : file.error ? <Box p={2}><ErrorPane error={file.error} /></Box> : (
          <Suspense fallback={<LoadingPane />}><TextEditor name={entry?.name ?? ''} value={value} onChange={setValue} /></Suspense>
        )}
      </DialogContent>
      {save.error && <Box px={3} pt={1}><ErrorPane error={save.error} /></Box>}
      <DialogActions style={{ backgroundColor: surface }}>
        <Button onClick={() => save.mutate()} variant="contained" disabled={!editable || save.isPending || file.isPending}>{t('files.save')}</Button>
      </DialogActions>
    </Dialog>
    <Dialog open={confirmClose} onClose={() => setConfirmClose(false)} maxWidth="xs" fullWidth>
      <DialogTitle>{t('files.unsavedTitle')}</DialogTitle>
      <DialogContent><Typography color="text.secondary">{t('files.unsavedBody')}</Typography>{save.error && <Box mt={2}><ErrorPane error={save.error} /></Box>}</DialogContent>
      <DialogActions><Button onClick={() => setConfirmClose(false)}>{t('files.keepEditing')}</Button><Button color="warning" onClick={discardAndClose}>{t('files.discardChanges')}</Button><Button variant="contained" disabled={save.isPending} onClick={() => save.mutate()}>{t('files.saveAndClose')}</Button></DialogActions>
    </Dialog>
    </>
  )
}

type PathAction = 'rename' | 'move' | 'copy'

interface CopyProgressState {
  copiedBytes: number
  totalBytes: number
  baseBytes: number
  startedAt: number
  updatedAt: number
  measuring: boolean
}

function parentPath(path: string) {
  return path.slice(0, Math.max(0, path.lastIndexOf('/'))) || '/'
}

function isMoveDestinationAllowed(destination: string, entry: FileEntry) {
  return entry.type !== 'directory' || (destination !== entry.path && !destination.startsWith(`${entry.path}/`))
}

export function PathActionDialog({ action, entries, copyDestination, onClose, onDone }: { action: PathAction | null; entries: FileEntry[]; copyDestination?: string; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [destination, setDestination] = useState('')
  const completed = useRef(new Set<string>())
  const conflictPath = useRef<string | null>(null)
  const copyPlan = useRef<{ bytes: Map<string, number>; totalBytes: number } | null>(null)
  const directCopyStarted = useRef(false)
  const [copyProgress, setCopyProgress] = useState<CopyProgressState | null>(null)
  const entry = entries[0] ?? null
  const multiple = entries.length > 1
  const directCopy = action === 'copy' && copyDestination !== undefined
  const entriesKey = entries.map((item) => item.path).join('\n')
  const destinationFolders = useQuery({
    queryKey: ['move-destination-folders', destination],
    queryFn: () => api.files.list(destination),
    enabled: action === 'move' && Boolean(destination),
  })
  useEffect(() => {
    if (!entry || !action) return
    const parent = parentPath(entry.path)
    setDestination(action === 'rename' ? entry.name : action === 'move' || multiple ? parent : `${parent === '/' ? '' : parent}/${entry.name}`)
  }, [action, entriesKey, entry, multiple])

  const mutate = useMutation({
    mutationFn: async (overwrite: boolean) => {
      if (!entry || !action) return
      let plan = copyPlan.current
      if (action === 'copy' && !plan) {
        setCopyProgress({ copiedBytes: 0, totalBytes: 0, baseBytes: 0, startedAt: 0, updatedAt: 0, measuring: true })
        const measured = await api.files.copySize(entries.map((item) => item.path))
        plan = { bytes: new Map(measured.items.map((item) => [item.source, item.bytes])), totalBytes: measured.totalBytes }
        copyPlan.current = plan
      }
      const completedBytes = action === 'copy' && plan
        ? entries.reduce((total, item) => total + (completed.current.has(item.path) ? (plan.bytes.get(item.path) ?? 0) : 0), 0)
        : 0
      if (action === 'copy' && plan) {
        const now = Date.now()
        setCopyProgress({ copiedBytes: completedBytes, totalBytes: plan.totalBytes, baseBytes: completedBytes, startedAt: now, updatedAt: now, measuring: false })
      }
      for (const item of entries) {
        if (completed.current.has(item.path)) continue
        const target = action === 'rename'
          ? `${parentPath(item.path)}/${destination}`.replaceAll('//', '/')
          : action === 'move' || multiple || directCopy ? joinPath(copyDestination ?? destination, item.name) : destination
        try {
          const replaceConflict = overwrite && conflictPath.current === item.path
          if (action === 'copy' && plan) {
            const itemBase = entries.reduce((total, candidate) => total + (completed.current.has(candidate.path) ? (plan.bytes.get(candidate.path) ?? 0) : 0), 0)
            const itemBytes = plan.bytes.get(item.path) ?? 0
            try {
              await api.files.copyWithProgress(item.path, target, replaceConflict, (copiedBytes) => {
                const now = Date.now()
                setCopyProgress((current) => current && { ...current, copiedBytes: itemBase + Math.min(copiedBytes, itemBytes), updatedAt: now })
              })
            } catch (error) {
              setCopyProgress((current) => current && { ...current, copiedBytes: itemBase, updatedAt: Date.now() })
              throw error
            }
          }
          else await api.files.move(item.path, target, replaceConflict)
          completed.current.add(item.path)
          if (action === 'copy' && plan) {
            const copiedBytes = entries.reduce((total, candidate) => total + (completed.current.has(candidate.path) ? (plan.bytes.get(candidate.path) ?? 0) : 0), 0)
            setCopyProgress((current) => current && { ...current, copiedBytes, updatedAt: Date.now() })
          }
          conflictPath.current = null
        } catch (error) {
          if (isConflictError(error)) conflictPath.current = item.path
          throw error
        }
      }
    },
    onSuccess: () => { completed.current.clear(); onDone(); onClose() },
  })
  const resetMutation = mutate.reset
  useEffect(() => {
    completed.current.clear()
    conflictPath.current = null
    copyPlan.current = null
    directCopyStarted.current = false
    setCopyProgress(null)
    resetMutation()
  }, [action, copyDestination, entriesKey, resetMutation])

  const startMutation = mutate.mutate
  useEffect(() => {
    if (!directCopy || !entry || directCopyStarted.current) return
    directCopyStarted.current = true
    startMutation(false)
  }, [directCopy, entry, startMutation])

  const submit = (event: FormEvent) => { event.preventDefault(); mutate.mutate(false) }
  const conflict = isConflictError(mutate.error)
  const percentage = copyProgress && copyProgress.totalBytes > 0 ? Math.min(100, Math.round(copyProgress.copiedBytes / copyProgress.totalBytes * 100)) : 0
  const activeCopiedBytes = copyProgress ? copyProgress.copiedBytes - copyProgress.baseBytes : 0
  const elapsedSeconds = copyProgress ? (copyProgress.updatedAt - copyProgress.startedAt) / 1000 : 0
  const etaSeconds = copyProgress && activeCopiedBytes > 0 && elapsedSeconds > 0 && copyProgress.copiedBytes < copyProgress.totalBytes
    ? (copyProgress.totalBytes - copyProgress.copiedBytes) / (activeCopiedBytes / elapsedSeconds)
    : 0
  const folders = destinationFolders.data?.entries.filter((item) => item.type === 'directory' && entries.every((source) => isMoveDestinationAllowed(item.path, source))) ?? []
  const movingToCurrentFolder = action === 'move' && entries.every((item) => parentPath(item.path) === destination)
  useCloseOnHistoryNavigation(Boolean(action && entry), onClose)
  return (
    <Dialog open={Boolean(action && entry)} onClose={mutate.isPending ? undefined : onClose} maxWidth="xs">
      <Stack component="form" onSubmit={submit}>
        <DialogTitle>{directCopy ? t('files.paste') : action ? multiple ? t('files.actionItems', { action: t(`files.${action}`), count: entries.length }) : t(`files.${action}`) : ''}</DialogTitle>
        <DialogContent sx={{ pt: 2, overflow: 'visible' }}>
          {multiple && <Typography color="text.secondary" mb={2}>{t('files.itemsSelected', { count: entries.length })}</Typography>}
          {action === 'move' ? <Stack gap={1.5}>
            <Typography variant="body2" color="text.secondary">{t('files.destinationFolder')}</Typography>
            <Typography aria-live="polite" className="file-name" fontFamily="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace">{destination}</Typography>
            <Box sx={{ border: 1, borderColor: 'divider', borderRadius: 1, maxHeight: 288, overflowY: 'auto' }}>
              <Button type="button" fullWidth color="inherit" startIcon={<ArrowBackRounded />} disabled={destination === '/'} onClick={() => { setDestination(parentPath(destination)); mutate.reset() }} sx={{ justifyContent: 'flex-start', borderRadius: 0 }}>..</Button>
              {destinationFolders.isPending ? <LoadingPane /> : destinationFolders.error ? <Box p={2}><ErrorPane error={destinationFolders.error} /></Box> : folders.length === 0 ? <Typography color="text.secondary" p={2}>{t('files.empty')}</Typography> : folders.map((folder) => <Button type="button" key={folder.path} fullWidth color="inherit" startIcon={<FolderRounded />} onClick={() => { setDestination(folder.path); mutate.reset() }} sx={{ justifyContent: 'flex-start', borderRadius: 0 }}>{folder.name}</Button>)}
            </Box>
          </Stack> : !directCopy && <TextField fullWidth autoFocus label={action === 'rename' ? t('files.name') : multiple ? t('files.destinationFolder') : t('files.destination')} value={destination} onChange={(event) => { setDestination(event.target.value); mutate.reset() }} error={Boolean(mutate.error)} helperText={mutate.error instanceof Error ? mutate.error.message : ''} />}
          {directCopy && mutate.error && <Box mb={2}><ErrorPane error={mutate.error} /></Box>}
          {action === 'copy' && mutate.isPending && copyProgress && <Stack gap={0.5} mt={2}>
            <Typography variant="body2">{copyProgress.measuring ? t('files.calculatingCopy') : t('files.copyingProgress', { copied: formatBytes(copyProgress.copiedBytes), total: formatBytes(copyProgress.totalBytes), progress: percentage })}</Typography>
            <LinearProgress aria-label={t('files.copyProgress')} variant={copyProgress.measuring ? 'indeterminate' : 'determinate'} value={percentage} />
            {etaSeconds > 0 && <Typography variant="caption" color="text.secondary">{t('files.copyEta', { eta: formatDuration(etaSeconds) })}</Typography>}
          </Stack>}
        </DialogContent>
        <DialogActions><Button onClick={onClose}>{t('common.cancel')}</Button>{conflict ? <Button color="warning" variant="contained" disabled={mutate.isPending} onClick={() => mutate.mutate(true)}>{t('files.replace')}</Button> : !directCopy && <Button type="submit" variant="contained" disabled={!destination || movingToCurrentFolder || mutate.isPending}>{t('common.confirm')}</Button>}</DialogActions>
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
  useCloseOnHistoryNavigation(Boolean(entry), onClose)
  return (
    <Dialog open={Boolean(entry)} onClose={create.isPending ? undefined : onClose} maxWidth="xs">
      <DialogTitle>{t('shares.create')}</DialogTitle>
      <DialogContent sx={{ pt: 2, overflow: 'visible' }}><Stack gap={2} pt={0.5}>
        <Typography color="text.secondary" className="file-name">{entry?.path}</Typography>
        <TextField label={t('shares.name')} value={name} onChange={(event) => setName(event.target.value)} />
        <PasswordField label={t('shares.password')} value={password} onChange={(event) => setPassword(event.target.value)} />
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
