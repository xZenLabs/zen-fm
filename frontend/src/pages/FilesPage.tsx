import { useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent as ReactDragEvent, type FormEvent, type MouseEvent } from 'react'
import {
  Alert, Box, Breadcrumbs, Button, Card, CardActionArea, CardContent,
  Dialog, DialogActions, DialogContent, DialogTitle, IconButton, InputAdornment,
  LinearProgress, Link, ListItemIcon, ListItemText, Menu, MenuItem, Snackbar, Stack, Table, TableBody,
  TableCell, TableContainer, TableHead, TableRow, TableSortLabel, TextField, Tooltip, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import FolderRounded from '@mui/icons-material/FolderRounded'
import InsertDriveFileRounded from '@mui/icons-material/InsertDriveFileRounded'
import DeviceUnknownRounded from '@mui/icons-material/DeviceUnknownRounded'
import SearchRounded from '@mui/icons-material/SearchRounded'
import UploadRounded from '@mui/icons-material/UploadRounded'
import NoteAddRounded from '@mui/icons-material/NoteAddRounded'
import CreateNewFolderRounded from '@mui/icons-material/CreateNewFolderRounded'
import GridViewRounded from '@mui/icons-material/GridViewRounded'
import ViewListRounded from '@mui/icons-material/ViewListRounded'
import MoreVertRounded from '@mui/icons-material/MoreVertRounded'
import OpenInNewRounded from '@mui/icons-material/OpenInNewRounded'
import EditRounded from '@mui/icons-material/EditRounded'
import EditDocumentIcon from '@mui/icons-material/EditDocument'
import DriveFileMoveRounded from '@mui/icons-material/DriveFileMoveRounded'
import ContentCopyRounded from '@mui/icons-material/ContentCopyRounded'
import ContentPasteRounded from '@mui/icons-material/ContentPasteRounded'
import DeleteOutlineRounded from '@mui/icons-material/DeleteOutlineRounded'
import DownloadRounded from '@mui/icons-material/DownloadRounded'
import ShareIcon from '@mui/icons-material/Share'
import FingerprintRounded from '@mui/icons-material/FingerprintRounded'
import RefreshRounded from '@mui/icons-material/RefreshRounded'
import CloseRounded from '@mui/icons-material/CloseRounded'
import { Link as RouterLink, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useTranslation } from 'react-i18next'
import { api, isConflictError, uploadResumable } from '../api/client'
import type { FileEntry, SortDirection, SortField } from '../api/types'
import { filesRoute, formatBytes, formatDate, formatDuration, formatShortDate, joinPath, publicShareUrl, TransferEtaEstimator } from '../utils'
import { ErrorPane, LoadingPane } from '../components/Feedback'
import { canEdit, CreateShareDialog, FileEditorDialog, FilePreviewDialog, PathActionDialog } from '../components/FileDialogs'
import { PageHeader } from '../components/PageHeader'

type ViewMode = 'grid' | 'list'
type PathAction = 'rename' | 'move' | 'copy'
type PathActionRequest = { action: PathAction; entries: FileEntry[]; copyDestination?: string }
type ConflictChoice = 'replace-all' | 'skip-all' | 'cancel'
type ConflictPolicy = 'ask' | 'replace' | 'skip'
type DroppedMove = { entry: FileEntry; destination: string }
type UploadFile = { file: File; relativePath: string }
type UploadBatch = { directories: string[]; files: UploadFile[] }
type UploadProgress = {
  name: string
  completedFiles: number
  totalFiles: number
  uploadedBytes: number
  totalBytes: number
  estimatedCompletionAt: number
}

const thumbnailExtensions = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'tif', 'tiff'])
const sortPreferenceKey = 'zenfm.files.sort'
const uploadConcurrency = 4
const uploadProgressThrottleMs = 100
const sortFields: SortField[] = ['name', 'size', 'modified']
const defaultSortPreference: { sort: SortField; direction: SortDirection } = { sort: 'name', direction: 'asc' }

function storedSortPreference(): { sort: SortField; direction: SortDirection } {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(sortPreferenceKey) ?? '')
    if (typeof value === 'object' && value !== null) {
      const preference = value as { sort?: unknown; direction?: unknown }
      if (typeof preference.sort === 'string' && sortFields.includes(preference.sort as SortField)
        && (preference.direction === 'asc' || preference.direction === 'desc')) {
        return { sort: preference.sort as SortField, direction: preference.direction }
      }
    }
  } catch {
    return defaultSortPreference
  }
  return defaultSortPreference
}

function hasDraggedFiles(dataTransfer: DataTransfer | null) {
  return Boolean(dataTransfer && Array.from(dataTransfer.types).includes('Files'))
}

function fileUploadBatch(files: File[]): UploadBatch {
  return { directories: [], files: files.map((file) => ({ file, relativePath: file.name })) }
}

function readFileEntry(entry: FileSystemFileEntry) {
  return new Promise<File>((resolve, reject) => entry.file(resolve, reject))
}

function readDirectoryEntries(entry: FileSystemDirectoryEntry) {
  const reader = entry.createReader()
  return new Promise<FileSystemEntry[]>((resolve, reject) => {
    const entries: FileSystemEntry[] = []
    const readNext = () => reader.readEntries((batch) => {
      if (batch.length === 0) {
        resolve(entries)
        return
      }
      entries.push(...batch)
      readNext()
    }, reject)
    readNext()
  })
}

async function collectDroppedEntry(entry: FileSystemEntry, parent: string, upload: UploadBatch) {
  const relativePath = parent ? `${parent}/${entry.name}` : entry.name
  if (entry.isDirectory) {
    upload.directories.push(relativePath)
    const children = await readDirectoryEntries(entry as FileSystemDirectoryEntry)
    for (const child of children) await collectDroppedEntry(child, relativePath, upload)
  } else if (entry.isFile) {
    upload.files.push({ file: await readFileEntry(entry as FileSystemFileEntry), relativePath })
  }
}

async function droppedUploadBatch(dataTransfer: DataTransfer): Promise<UploadBatch> {
  const entries = Array.from(dataTransfer.items ?? [])
    .filter((item) => item.kind === 'file')
    .map((item) => typeof item.webkitGetAsEntry === 'function' ? item.webkitGetAsEntry() : null)
    .filter((entry): entry is FileSystemEntry => entry !== null)
  if (entries.length === 0) return fileUploadBatch(Array.from(dataTransfer.files))

  const upload: UploadBatch = { directories: [], files: [] }
  for (const entry of entries) await collectDroppedEntry(entry, '', upload)
  return upload
}

function folderLabel(path: string) {
  return path === '/' ? 'Home' : path.replace(/^\/+/, '')
}

function FileArtwork({ entry }: { entry: FileEntry }) {
  const [failed, setFailed] = useState(false)
  const extension = entry.name.split('.').pop()?.toLowerCase() ?? ''
  const image = entry.type === 'file' && thumbnailExtensions.has(extension)
  if (!image || failed) return iconFor(entry)
  return <img className="file-thumbnail" src={api.files.previewUrl(entry.path, 360, 240)} loading="lazy" alt={entry.name} onError={() => setFailed(true)} />
}

function iconFor(entry: FileEntry) {
  if (entry.type === 'directory') return <FolderRounded color="primary" />
  if (entry.type === 'file') return <InsertDriveFileRounded color="action" />
  return <DeviceUnknownRounded color="disabled" />
}

function canPasteInto(destination: string, entry: FileEntry) {
  const target = joinPath(destination, entry.name)
  return target !== entry.path && (entry.type !== 'directory' || !target.startsWith(`${entry.path}/`))
}

export function FilesPage() {
  const { t } = useTranslation()
  const params = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const theme = useTheme()
  const mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const uploadInput = useRef<HTMLInputElement>(null)
  const uploadAbort = useRef<AbortController | null>(null)
  const path = `/${params['*'] ?? ''}`.replaceAll('//', '/')
  const [view, setView] = useState<ViewMode>(() => mobile ? 'grid' : 'list')
  const [sortPreference, setSortPreference] = useState(storedSortPreference)
  const { sort, direction } = sortPreference
  const [showHidden, setShowHidden] = useState(false)
  const [searchDraft, setSearchDraft] = useState('')
  const [searchTerm, setSearchTerm] = useState('')
  const [menuAnchor, setMenuAnchor] = useState<HTMLElement | null>(null)
  const [menuPosition, setMenuPosition] = useState<{ top: number; left: number } | null>(null)
  const [selected, setSelected] = useState<FileEntry | null>(null)
  const [clipboard, setClipboard] = useState<FileEntry[]>([])
  const [preview, setPreview] = useState<FileEntry | null>(null)
  const [editor, setEditor] = useState<FileEntry | null>(null)
  const [pathAction, setPathAction] = useState<PathActionRequest | null>(null)
  const [sharing, setSharing] = useState<FileEntry | null>(null)
  const [newFileOpen, setNewFileOpen] = useState(false)
  const [fileName, setFileName] = useState('')
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [folderName, setFolderName] = useState('')
  const [deleting, setDeleting] = useState<FileEntry[]>([])
  const [notice, setNotice] = useState('')
  const [upload, setUpload] = useState<UploadProgress | null>(null)
  const [uploadClock, setUploadClock] = useState(() => Date.now())
  const [dropTarget, setDropTarget] = useState<string | null>(null)
  const [droppedMove, setDroppedMove] = useState<DroppedMove | null>(null)
  const [conflict, setConflict] = useState<File | null>(null)
  const conflictResolver = useRef<((choice: ConflictChoice) => void) | null>(null)
  const droppedFilesHandler = useRef<(dataTransfer: DataTransfer, destination: string) => void>(() => undefined)
  const draggedEntry = useRef<FileEntry | null>(null)
  const selectionAnchor = useRef<string | null>(null)
  const selectionPath = useRef(path)
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(() => new Set())

  const listing = useQuery({ queryKey: ['files', path, showHidden], queryFn: () => api.files.list(path, showHidden) })
  const preferences = useQuery({ queryKey: ['settings'], queryFn: api.settings.get })
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage })
  const search = useQuery({
    queryKey: ['search', path, searchTerm, showHidden],
    queryFn: () => api.search(path, searchTerm, showHidden),
    enabled: searchTerm.length >= 2,
  })

  useEffect(() => {
    if (preferences.data) setShowHidden(preferences.data.showHidden)
  }, [preferences.data])

  useEffect(() => {
    if (mobile) setView((current) => current === 'list' ? 'grid' : current)
  }, [mobile])

  useEffect(() => {
    try { localStorage.setItem(sortPreferenceKey, JSON.stringify(sortPreference)) } catch {
      // Private browsing may disable persistent storage.
    }
  }, [sortPreference])

  useEffect(() => {
    if (selectionPath.current === path) return
    selectionPath.current = path
    selectionAnchor.current = null
    setSelected(null)
    setSelectedPaths(new Set())
  }, [path])

  const uploadActive = Boolean(upload)
  useEffect(() => {
    if (!uploadActive) return
    setUploadClock(Date.now())
    const interval = window.setInterval(() => setUploadClock(Date.now()), 1_000)
    return () => window.clearInterval(interval)
  }, [uploadActive])

  useEffect(() => () => uploadAbort.current?.abort(), [])

  useEffect(() => {
    const openPageContextMenu = (event: globalThis.MouseEvent) => {
      const target = event.target
      if (!(target instanceof Element)) return
      const pageBackground = target === document.documentElement
        || target === document.body
        || target.id === 'root'
        || Boolean(target.closest('.app-shell'))
      if (!pageBackground || target.closest('.file-drop-zone')) return
      if (target.closest('header, nav, a, button, input, textarea, [contenteditable="true"]')) return
      event.preventDefault()
      setSelected(null)
      setMenuAnchor(null)
      setMenuPosition({ top: event.clientY, left: event.clientX })
    }
    document.addEventListener('contextmenu', openPageContextMenu)
    return () => document.removeEventListener('contextmenu', openPageContextMenu)
  }, [])

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['files', path] })
  const entries = useMemo(() => {
    const source = searchTerm ? search.data?.entries ?? [] : listing.data?.entries ?? []
    const visible = source.filter((entry) => showHidden || !entry.hidden && !entry.name.startsWith('.'))
    return [...visible].sort((left, right) => {
      if (left.type === 'directory' && right.type !== 'directory') return -1
      if (left.type !== 'directory' && right.type === 'directory') return 1
      const comparison = sort === 'size' ? left.size - right.size
        : sort === 'modified' ? new Date(left.modifiedAt).valueOf() - new Date(right.modifiedAt).valueOf()
          : left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: 'base' })
      return direction === 'asc' ? comparison : -comparison
    })
  }, [direction, listing.data?.entries, search.data?.entries, searchTerm, showHidden, sort])
  const selectableEntries = useMemo(() => entries.filter((entry) => entry.type === 'file' || entry.type === 'directory'), [entries])
  const selectedEntries = useMemo(() => selectableEntries.filter((entry) => selectedPaths.has(entry.path)), [selectableEntries, selectedPaths])
  const menuEntries = selected && selected.type !== 'special'
    ? selectedPaths.has(selected.path) ? selectedEntries : [selected]
    : []
  const menuActionLabel = (action: 'download' | 'move' | 'copy' | 'delete') => menuEntries.length > 1
    ? t('files.actionItems', { action: t(`files.${action}`), count: menuEntries.length })
    : t(`files.${action}`)

  const createFolder = useMutation({
    mutationFn: () => api.files.createDirectory(joinPath(path, folderName)),
    onSuccess: () => { setNewFolderOpen(false); setFolderName(''); refresh() },
  })
  const createFile = useMutation({
    mutationFn: () => api.files.createText(joinPath(path, fileName)),
    onSuccess: () => {
      const createdPath = joinPath(path, fileName)
      setNewFileOpen(false)
      setFileName('')
      refresh()
      setEditor({ name: createdPath.split('/').pop() ?? createdPath, path: createdPath, type: 'file', size: 0, modifiedAt: new Date().toISOString(), mimeType: 'text/plain' })
    },
  })
  const remove = useMutation({
    mutationFn: (targets: FileEntry[]) => Promise.all(targets.map((entry) => api.files.remove(entry.path, entry.type === 'directory'))),
    onSuccess: () => {
      setDeleting([])
      setSelected(null)
      setSelectedPaths(new Set())
      selectionAnchor.current = null
      refresh()
    },
  })
  const moveDroppedEntry = useMutation({
    mutationFn: ({ entry, destination, overwrite }: DroppedMove & { overwrite: boolean }) => api.files.move(entry.path, joinPath(destination, entry.name), overwrite),
    onSuccess: () => { setDroppedMove(null); refresh() },
  })

  const openEntry = (entry: FileEntry) => {
    if (entry.type === 'directory') void navigate(filesRoute(entry.path))
    else if (entry.type === 'file') setPreview(entry)
  }
  const prepareItemMenu = (entry: FileEntry) => {
    setSelected(entry)
    if (entry.type === 'special') {
      setSelectedPaths(new Set())
      selectionAnchor.current = null
    } else if (!selectedPaths.has(entry.path)) {
      setSelectedPaths(new Set([entry.path]))
      selectionAnchor.current = entry.path
    }
  }
  const openMenu = (event: MouseEvent<HTMLElement>, entry: FileEntry) => {
    event.preventDefault(); event.stopPropagation(); prepareItemMenu(entry); setMenuPosition(null); setMenuAnchor(event.currentTarget)
  }
  const openContextMenu = (event: MouseEvent<HTMLElement>, entry: FileEntry) => {
    event.preventDefault(); event.stopPropagation(); prepareItemMenu(entry); setMenuAnchor(null); setMenuPosition({ top: event.clientY, left: event.clientX })
  }
  const openFolderMenu = (event: MouseEvent<HTMLElement>) => {
    const target = event.target
    if (target instanceof Element && target.closest('a, button, input, textarea, [contenteditable="true"]')) return
    event.preventDefault(); setSelected(null); setMenuAnchor(null); setMenuPosition({ top: event.clientY, left: event.clientX })
  }
  const closeMenu = () => { setMenuAnchor(null); setMenuPosition(null) }
  const beginAction = (action: PathAction, targets = menuEntries) => {
    if (targets.length === 0) return
    setPathAction({ action, entries: [...targets] })
    closeMenu()
  }
  const copyToClipboard = (targets = menuEntries) => {
    if (targets.length === 0) return
    setClipboard([...targets])
    setNotice(t('common.copied'))
    closeMenu()
  }
  const pasteClipboard = (destination: string) => {
    if (clipboard.length === 0) return
    setPathAction({ action: 'copy', entries: [...clipboard], copyDestination: destination })
    closeMenu()
  }
  const beginDelete = (targets = menuEntries) => {
    if (targets.length === 0) return
    remove.reset()
    setDeleting([...targets])
    closeMenu()
  }

  const askAboutConflict = (file: File, signal: AbortSignal) => new Promise<ConflictChoice>((resolve) => {
    const finish = (choice: ConflictChoice) => {
      signal.removeEventListener('abort', abort)
      resolve(choice)
    }
    const abort = () => finish('cancel')
    if (signal.aborted) return abort()
    signal.addEventListener('abort', abort, { once: true })
    conflictResolver.current = finish
    setConflict(file)
  })
  const resolveConflict = (choice: ConflictChoice) => {
    conflictResolver.current?.(choice)
    conflictResolver.current = null
    setConflict(null)
  }
  const uploadFile = async (file: File, destination: string, overwrite: boolean, onProgress: (sent: number) => void, signal: AbortSignal) => {
    if (file.size < 8 * 1024 * 1024) {
      await api.files.uploadWithProgress(destination, file, overwrite, (sent) => onProgress(sent), signal)
      return
    }
    await new Promise<void>((resolve, reject) => uploadResumable(destination, file, {
      onProgress: (sent) => { if (!signal.aborted) onProgress(sent) },
      onSuccess: resolve,
      onError: reject,
    }, overwrite, signal))
  }

  const uploadFiles = async (upload: UploadBatch, destinationPath: string, controller: AbortController) => {
    const { signal } = controller
    let conflictPolicy: ConflictPolicy = 'ask'
    const totalBytes = upload.files.reduce((total, item) => total + item.file.size, 0)
    const totalFiles = upload.files.length
    let completedFiles = 0
    let nextFile = 0
    let cancelled = false
    let conflictQueue = Promise.resolve()
    const displayProgress = new Array<number>(totalFiles).fill(0)
    const transferredProgress = new Array<number>(totalFiles).fill(0)
    const eta = new TransferEtaEstimator()
    let uploadedBytes = 0
    let transferredBytes = 0
    let currentName = upload.files[0]?.file.name ?? ''
    let progressTimer: number | undefined
    const clearProgressTimer = () => {
      window.clearTimeout(progressTimer)
      progressTimer = undefined
    }
    const flushProgress = () => {
      clearProgressTimer()
      if (signal.aborted) return
      const estimatedCompletionAt = eta.update(transferredBytes, totalBytes - uploadedBytes, Date.now())
      setUpload({ name: currentName, completedFiles, totalFiles, uploadedBytes, totalBytes, estimatedCompletionAt })
    }
    const scheduleProgress = (immediate: boolean) => {
      if (immediate) {
        flushProgress()
      } else if (progressTimer === undefined) {
        progressTimer = window.setTimeout(flushProgress, uploadProgressThrottleMs)
      }
    }
    const updateProgress = (index: number, file: File, sent: number, transferred = true, immediate = false) => {
      if (signal.aborted) return
      const previousDisplay = displayProgress[index] ?? 0
      const bounded = Math.max(previousDisplay, Math.min(sent, file.size))
      displayProgress[index] = bounded
      uploadedBytes += bounded - previousDisplay
      if (transferred) {
        const previousTransferred = transferredProgress[index] ?? 0
        const nextTransferred = Math.max(previousTransferred, bounded)
        transferredProgress[index] = nextTransferred
        transferredBytes += nextTransferred - previousTransferred
      }
      currentName = file.name
      scheduleProgress(immediate)
    }
    if (upload.files[0]) updateProgress(0, upload.files[0].file, 0, true, true)
    for (const directory of upload.directories) {
      try {
        await api.files.createDirectory(joinPath(destinationPath, directory), signal)
      } catch (error) {
        // Dropping a folder onto an existing tree merges it; file conflicts are
        // still resolved individually below.
        if (!isConflictError(error)) throw error
      }
    }
    const decideConflict = (file: File) => {
      const decision = conflictQueue.then(async (): Promise<ConflictPolicy | 'cancel'> => {
        if (cancelled || signal.aborted) return 'cancel'
        if (conflictPolicy !== 'ask') return conflictPolicy
        const choice = await askAboutConflict(file, signal)
        if (choice === 'cancel' && signal.aborted) {
          resolveConflict('cancel')
          return 'cancel'
        }
        if (choice === 'cancel') {
          cancelled = true
          controller.abort()
          return 'cancel'
        }
        conflictPolicy = choice === 'replace-all' ? 'replace' : 'skip'
        return conflictPolicy
      })
      conflictQueue = decision.then(() => undefined, () => undefined)
      return decision
    }
    const uploadOne = async (index: number) => {
      const item = upload.files[index]
      if (!item) return
      const { file, relativePath } = item
      const destination = joinPath(destinationPath, relativePath)
      let overwrite = conflictPolicy === 'replace'
      let succeeded = false
      for (;;) {
        if (cancelled || signal.aborted) return
        updateProgress(index, file, 0)
        try {
          await uploadFile(file, destination, overwrite, (sent) => updateProgress(index, file, sent), signal)
          succeeded = true
          break
        } catch (error) {
          if (!isConflictError(error)) {
            cancelled = true
            if (!signal.aborted) controller.abort(error)
            throw error
          }
          const decision = await decideConflict(file)
          if (decision === 'cancel') return
          if (decision === 'skip') {
            updateProgress(index, file, file.size, false)
            break
          }
          overwrite = true
        }
      }
      completedFiles++
      updateProgress(index, file, file.size, succeeded, true)
    }
    const worker = async () => {
      while (!cancelled && !signal.aborted) {
        const index = nextFile++
        if (index >= upload.files.length) return
        await uploadOne(index)
      }
    }
    const results = await Promise.allSettled(Array.from({ length: Math.min(uploadConcurrency, totalFiles) }, () => worker()))
    clearProgressTimer()
    if (signal.aborted && signal.reason instanceof Error && signal.reason.name !== 'AbortError') throw signal.reason
    const failed = results.find((result): result is PromiseRejectedResult => result.status === 'rejected')
    if (failed) throw failed.reason
    refresh()
  }

  const safeUploadFiles = async (upload: UploadBatch, destinationPath: string) => {
    if (upload.directories.length === 0 && upload.files.length === 0) return
    const controller = new AbortController()
    uploadAbort.current?.abort()
    uploadAbort.current = controller
    try {
      await uploadFiles(upload, destinationPath, controller)
    } catch (error) {
      if (!(error instanceof Error && error.name === 'AbortError')) setNotice(error instanceof Error ? error.message : t('common.error'))
    } finally {
      if (uploadAbort.current === controller) {
        uploadAbort.current = null
        setUpload(null)
      }
    }
  }

  const cancelUpload = () => {
    uploadAbort.current?.abort()
    if (conflictResolver.current) resolveConflict('cancel')
  }

  const chooseUploadFiles = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ''
    await safeUploadFiles(fileUploadBatch(files), path)
  }

  droppedFilesHandler.current = (dataTransfer, destination) => {
    void droppedUploadBatch(dataTransfer)
      .then((upload) => safeUploadFiles(upload, destination))
      .catch((error: unknown) => setNotice(error instanceof Error ? error.message : t('common.error')))
  }

  useEffect(() => {
    const dragOver = (event: globalThis.DragEvent) => {
      if (!hasDraggedFiles(event.dataTransfer)) return
      event.preventDefault()
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
      setDropTarget(path)
    }
    const drop = (event: globalThis.DragEvent) => {
      if (!hasDraggedFiles(event.dataTransfer)) return
      event.preventDefault()
      setDropTarget(null)
      if (event.dataTransfer) droppedFilesHandler.current(event.dataTransfer, path)
    }
    const dragLeave = (event: globalThis.DragEvent) => {
      if (event.relatedTarget === null) setDropTarget(null)
    }
    const dragEnd = () => setDropTarget(null)
    window.addEventListener('dragover', dragOver)
    window.addEventListener('drop', drop)
    window.addEventListener('dragleave', dragLeave)
    window.addEventListener('dragend', dragEnd)
    return () => {
      window.removeEventListener('dragover', dragOver)
      window.removeEventListener('drop', drop)
      window.removeEventListener('dragleave', dragLeave)
      window.removeEventListener('dragend', dragEnd)
    }
  }, [path])

  const canMoveTo = (entry: FileEntry, destination: string) => entry.type !== 'special'
    && joinPath(destination, entry.name) !== entry.path
    && (entry.type !== 'directory' || destination !== entry.path && !destination.startsWith(`${entry.path}/`))

  const startMoveDrag = (event: ReactDragEvent<HTMLElement>, entry: FileEntry) => {
    draggedEntry.current = entry
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('application/x-zenfm-entry', entry.path)
  }

  const endMoveDrag = () => {
    draggedEntry.current = null
    setDropTarget(null)
  }

  const rejectMoveDrop = (event: ReactDragEvent<HTMLElement>) => {
    if (!draggedEntry.current) return
    event.preventDefault()
    event.stopPropagation()
    setDropTarget(null)
  }

  const prepareDrop = (event: ReactDragEvent<HTMLElement>, destination: string) => {
    const entry = draggedEntry.current
    if (entry) {
      event.preventDefault()
      event.stopPropagation()
      if (!canMoveTo(entry, destination)) {
        setDropTarget(null)
        return
      }
      event.dataTransfer.dropEffect = 'move'
      setDropTarget(destination)
      return
    }
    if (!hasDraggedFiles(event.dataTransfer)) return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = 'copy'
    setDropTarget(destination)
  }

  const acceptDrop = (event: ReactDragEvent<HTMLElement>, destination: string) => {
    const entry = draggedEntry.current
    if (entry) {
      event.preventDefault()
      event.stopPropagation()
      draggedEntry.current = null
      setDropTarget(null)
      if (canMoveTo(entry, destination)) {
        moveDroppedEntry.reset()
        setDroppedMove({ entry, destination })
      }
      return
    }
    if (!hasDraggedFiles(event.dataTransfer)) return
    event.preventDefault()
    event.stopPropagation()
    setDropTarget(null)
    droppedFilesHandler.current(event.dataTransfer, destination)
  }

  const selectEntry = (event: MouseEvent<HTMLElement>, entry: FileEntry) => {
    if (entry.type === 'special') {
      setSelected(entry)
      setSelectedPaths(new Set())
      selectionAnchor.current = null
      return
    }
    const toggle = event.ctrlKey || event.metaKey
    const anchorIndex = selectionAnchor.current === null ? -1 : selectableEntries.findIndex((item) => item.path === selectionAnchor.current)
    const entryIndex = selectableEntries.findIndex((item) => item.path === entry.path)
    setSelected(entry)

    if (event.shiftKey && anchorIndex >= 0 && entryIndex >= 0) {
      const start = Math.min(anchorIndex, entryIndex)
      const end = Math.max(anchorIndex, entryIndex)
      const range = selectableEntries.slice(start, end + 1).map((item) => item.path)
      setSelectedPaths((current) => new Set(toggle ? [...current, ...range] : range))
      return
    }
    selectionAnchor.current = entry.path
    if (toggle) {
      setSelectedPaths((current) => {
        const next = new Set(current)
        if (next.has(entry.path)) next.delete(entry.path)
        else next.add(entry.path)
        return next
      })
      return
    }
    setSelectedPaths(new Set([entry.path]))
  }

  const startArchiveDownload = async (paths: string[], filename: string) => {
    try {
      const ticket = await api.files.createArchiveTicket(paths)
      const link = document.createElement('a')
      link.href = ticket.url
      link.download = filename
      link.click()
    } catch (error) {
      setNotice(error instanceof Error ? error.message : t('common.error'))
    }
  }

  const downloadEntries = (targets: FileEntry[]) => {
    if (targets.length === 0) return
    if (targets.length === 1 && targets[0]?.type === 'file') {
      const link = document.createElement('a')
      link.href = api.files.rawUrl(targets[0].path)
      link.download = targets[0].name
      link.click()
      return
    }
    const filename = targets.length === 1 ? `${targets[0]?.name ?? 'zenfm-selection'}.zip` : 'zenfm-selection.zip'
    void startArchiveDownload(targets.map((entry) => entry.path), filename)
  }

  const submitSearch = (event: FormEvent) => {
    event.preventDefault()
    setSearchTerm(searchDraft.trim())
  }

  const sortBy = (field: SortField) => {
    setSortPreference((current) => current.sort === field
      ? { ...current, direction: current.direction === 'asc' ? 'desc' : 'asc' }
      : { sort: field, direction: 'asc' })
  }

  const breadcrumbs = path.split('/').filter(Boolean)
  const disk = listing.data?.disk ?? usage.data
  const uploadPercentage = upload && upload.totalBytes > 0 ? Math.min(100, Math.round(upload.uploadedBytes / upload.totalBytes * 100)) : 0
  const uploadEtaSeconds = upload?.estimatedCompletionAt ? Math.max(0, (upload.estimatedCompletionAt - uploadClock) / 1_000) : 0

  return (
    <Box className={`file-drop-zone${dropTarget === path ? ' drop-active' : ''}`} onContextMenu={openFolderMenu} onDragOver={(event) => prepareDrop(event, path)} onDrop={(event) => acceptDrop(event, path)} sx={{ flex: 1, minWidth: 0 }}>
      <Stack gap={2.5}>
        <PageHeader title={searchTerm ? t('files.searchResults') : t('nav.files')} actions={<Stack direction="row" gap={1} flexWrap="wrap">
            <input ref={uploadInput} type="file" multiple hidden onChange={(event) => void chooseUploadFiles(event)} />
            <Button variant="contained" startIcon={<UploadRounded />} disabled={uploadActive} onClick={() => uploadInput.current?.click()}>{t('files.upload')}</Button>
            <Button variant="outlined" startIcon={<NoteAddRounded />} onClick={() => setNewFileOpen(true)}>{t('files.newFile')}</Button>
            <Button variant="outlined" startIcon={<CreateNewFolderRounded />} onClick={() => setNewFolderOpen(true)}>{t('files.newFolder')}</Button>
          </Stack>}>
          <Breadcrumbs aria-label="Breadcrumb">
            <Link component={RouterLink} underline="hover" color="inherit" to="/files">Home</Link>
            {breadcrumbs.map((part, index) => {
              const target = `/${breadcrumbs.slice(0, index + 1).join('/')}`
              return <Link key={target} component={RouterLink} underline="hover" color={index === breadcrumbs.length - 1 ? 'text.primary' : 'inherit'} to={filesRoute(target)}>{part}</Link>
            })}
          </Breadcrumbs>
        </PageHeader>

        <Card variant="outlined"><CardContent sx={{ p: { xs: 1.5, sm: 2 }, '&:last-child': { pb: { xs: 1.5, sm: 2 } } }}>
          <Stack direction={{ xs: 'column', lg: 'row' }} gap={1.5} alignItems={{ lg: 'center' }}>
            <TextField component="form" onSubmit={submitSearch} value={searchDraft} onChange={(event) => { setSearchDraft(event.target.value); if (!event.target.value) setSearchTerm('') }} placeholder={t('files.search')} sx={{ flex: 1, minWidth: 220 }} inputProps={{ 'aria-label': t('files.search') }} InputProps={{ startAdornment: <InputAdornment position="start"><SearchRounded /></InputAdornment>, endAdornment: searchDraft ? <InputAdornment position="end"><IconButton edge="end" size="small" aria-label={t('files.clearSearch')} onClick={() => { setSearchDraft(''); setSearchTerm('') }} sx={{ width: 32, height: 32, minWidth: 32, minHeight: 32 }}><CloseRounded /></IconButton></InputAdornment> : undefined }} />
            <Stack direction="row" gap={1} alignItems="center" flexWrap="wrap">
              <Tooltip title={t('files.refresh')}><IconButton onClick={refresh}><RefreshRounded /></IconButton></Tooltip>
              <Box role="group" aria-label="View" sx={{ display: 'flex', gap: 0.25, p: 0.375, borderRadius: 999, bgcolor: 'action.hover' }}>
                <IconButton size="small" aria-label={t('files.grid')} aria-pressed={view === 'grid'} onClick={() => setView('grid')} sx={{ borderRadius: 999, bgcolor: view === 'grid' ? 'background.paper' : 'transparent', boxShadow: view === 'grid' ? 1 : 0, '&:hover': { bgcolor: view === 'grid' ? 'background.paper' : 'action.selected' } }}><GridViewRounded /></IconButton>
                <IconButton size="small" aria-label={t('files.list')} aria-pressed={view === 'list'} onClick={() => setView('list')} sx={{ borderRadius: 999, bgcolor: view === 'list' ? 'background.paper' : 'transparent', boxShadow: view === 'list' ? 1 : 0, '&:hover': { bgcolor: view === 'list' ? 'background.paper' : 'action.selected' } }}><ViewListRounded /></IconButton>
              </Box>
            </Stack>
          </Stack>
        </CardContent></Card>

        {upload && <Alert icon={<UploadRounded />} action={<Button color="inherit" size="small" onClick={cancelUpload}>{t('common.cancel')}</Button>}><Stack width="100%" gap={0.5}>
          <Typography>{t('files.uploadingBatch', { completed: upload.completedFiles, count: upload.totalFiles, name: upload.name })}</Typography>
          <Typography variant="caption" color="text.secondary">{t('files.uploadingProgress', { uploaded: formatBytes(upload.uploadedBytes), total: formatBytes(upload.totalBytes), progress: uploadPercentage })}</Typography>
          <LinearProgress aria-label={t('files.uploadProgress')} variant="determinate" value={uploadPercentage} />
          {uploadEtaSeconds > 0 && <Typography variant="caption" color="text.secondary">{t('files.uploadEta', { eta: formatDuration(uploadEtaSeconds) })}</Typography>}
        </Stack></Alert>}
        {disk && <Typography variant="caption" color="text.secondary">{formatBytes(disk.used)} of {formatBytes(disk.total)} used</Typography>}
        <Box className="file-listing" minHeight="calc(100dvh - 370px)">
          {(listing.isPending || search.isFetching) ? <LoadingPane /> : listing.error ? <ErrorPane error={listing.error} retry={refresh} /> : search.error ? <ErrorPane error={search.error} /> : entries.length === 0 ? (
            <Box textAlign="center" py={10}><FolderRounded sx={{ fontSize: 48, color: 'text.disabled', mb: 1 }} /><Typography variant="h2">{t('files.empty')}</Typography><Typography color="text.secondary" mt={0.5}>{t('files.emptyHint')}</Typography></Box>
          ) : view === 'grid' ? (
            <Box role="list" display="grid" gridTemplateColumns={{ xs: '1fr', sm: 'repeat(2, minmax(0, 1fr))', md: 'repeat(3, minmax(0, 1fr))', lg: 'repeat(4, minmax(0, 1fr))', xl: 'repeat(6, minmax(0, 1fr))' }} gap={1.5}>
              {entries.map((entry) => <Card key={entry.path} role="listitem" aria-label={entry.name} variant="outlined" className={`file-card${selectedPaths.has(entry.path) ? ' selected' : ''}${dropTarget === entry.path ? ' drop-target' : ''}`} onContextMenu={(event) => openContextMenu(event, entry)} onDragOver={entry.type === 'directory' ? (event) => prepareDrop(event, entry.path) : undefined} onDrop={entry.type === 'directory' ? (event) => acceptDrop(event, entry.path) : undefined}>
                <CardActionArea component="div" onClick={(event) => selectEntry(event, entry)} onDoubleClick={() => openEntry(entry)} disabled={entry.type === 'special'}>
                  <CardContent><Stack direction="row" alignItems="center" gap={1.25}><FileArtwork entry={entry} /><Box minWidth={0} flex={1}><Typography fontWeight={600} className="file-name" title={entry.name}>{entry.name}</Typography><Typography variant="caption" color="text.secondary">{formatBytes(entry.size)} · {formatShortDate(entry.modifiedAt)}</Typography></Box><IconButton size="small" sx={{ ml: -0.75 }} aria-label={`Actions for ${entry.name}`} onClick={(event) => openMenu(event, entry)} onDoubleClick={(event) => event.stopPropagation()}><MoreVertRounded /></IconButton></Stack></CardContent>
                </CardActionArea>
              </Card>)}
            </Box>
          ) : (
            <Card variant="outlined"><TableContainer><Table size="small" aria-label={t('nav.files')} sx={{ minWidth: 640 }}>
              <TableHead onContextMenu={(event) => event.stopPropagation()}><TableRow>
                <TableCell sortDirection={sort === 'name' ? direction : false}><TableSortLabel active={sort === 'name'} direction={sort === 'name' ? direction : 'asc'} hideSortIcon={false} onClick={() => sortBy('name')}>{t('files.name')}</TableSortLabel></TableCell>
                <TableCell align="right" sortDirection={sort === 'size' ? direction : false}><TableSortLabel active={sort === 'size'} direction={sort === 'size' ? direction : 'asc'} hideSortIcon={false} onClick={() => sortBy('size')}>{t('files.size')}</TableSortLabel></TableCell>
                <TableCell sortDirection={sort === 'modified' ? direction : false}><TableSortLabel active={sort === 'modified'} direction={sort === 'modified' ? direction : 'asc'} hideSortIcon={false} onClick={() => sortBy('modified')}>{t('files.modified')}</TableSortLabel></TableCell>
                <TableCell aria-label="Actions" />
              </TableRow></TableHead>
              <TableBody>{entries.map((entry) => <TableRow key={entry.path} hover draggable={entry.type !== 'special'} selected={selectedPaths.has(entry.path)} className={`file-row${selectedPaths.has(entry.path) ? ' selected' : ''}${dropTarget === entry.path ? ' drop-target' : ''}`} onClick={(event) => selectEntry(event, entry)} onDoubleClick={() => openEntry(entry)} onContextMenu={(event) => openContextMenu(event, entry)} onDragStart={(event) => startMoveDrag(event, entry)} onDragEnd={endMoveDrag} onDragOver={entry.type === 'directory' ? (event) => prepareDrop(event, entry.path) : rejectMoveDrop} onDrop={entry.type === 'directory' ? (event) => acceptDrop(event, entry.path) : rejectMoveDrop} sx={{ cursor: entry.type !== 'special' ? 'pointer' : 'default' }}>
                <TableCell><Stack direction="row" alignItems="center" gap={1.25} minWidth={200}>{iconFor(entry)}<Typography fontWeight={600} className="file-name" minWidth={0} flex={1}>{entry.name}</Typography></Stack></TableCell>
                <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>{entry.type === 'directory' ? '—' : formatBytes(entry.size)}</TableCell>
                <TableCell sx={{ whiteSpace: 'nowrap' }}>{formatDate(entry.modifiedAt)}</TableCell>
                <TableCell align="right"><IconButton aria-label={`Actions for ${entry.name}`} onClick={(event) => openMenu(event, entry)} onDoubleClick={(event) => event.stopPropagation()}><MoreVertRounded /></IconButton></TableCell>
              </TableRow>)}</TableBody>
            </Table></TableContainer></Card>
          )}
        </Box>
      </Stack>

      <Menu anchorEl={menuAnchor} anchorReference={menuPosition ? 'anchorPosition' : 'anchorEl'} anchorPosition={menuPosition ?? undefined} open={Boolean(menuAnchor || menuPosition)} onClose={closeMenu}>
        {!selected && <MenuItem onClick={() => { setNewFileOpen(true); closeMenu() }}><ListItemIcon><NoteAddRounded /></ListItemIcon><ListItemText>{t('files.newFile')}</ListItemText></MenuItem>}
        {!selected && <MenuItem onClick={() => { setNewFolderOpen(true); closeMenu() }}><ListItemIcon><CreateNewFolderRounded /></ListItemIcon><ListItemText>{t('files.newFolder')}</ListItemText></MenuItem>}
        {!selected && clipboard.length > 0 && <MenuItem disabled={!clipboard.every((entry) => canPasteInto(path, entry))} onClick={() => pasteClipboard(path)}><ListItemIcon><ContentPasteRounded /></ListItemIcon><ListItemText>{t('files.paste')}</ListItemText></MenuItem>}
        {menuEntries.length === 1 && selected?.type === 'file' && <MenuItem onClick={() => { setPreview(selected); closeMenu() }}><ListItemIcon><OpenInNewRounded /></ListItemIcon><ListItemText>{t('files.preview')}</ListItemText></MenuItem>}
        {menuEntries.length === 1 && selected && canEdit(selected) && <MenuItem onClick={() => { setEditor(selected); closeMenu() }}><ListItemIcon><EditDocumentIcon /></ListItemIcon><ListItemText>{t('files.edit')}</ListItemText></MenuItem>}
        {menuEntries.length === 1 && <MenuItem onClick={() => beginAction('rename')}><ListItemIcon><EditRounded /></ListItemIcon><ListItemText>{t('files.rename')}</ListItemText></MenuItem>}
        {menuEntries.length > 0 && <MenuItem onClick={() => beginAction('move')}><ListItemIcon><DriveFileMoveRounded /></ListItemIcon><ListItemText>{menuActionLabel('move')}</ListItemText></MenuItem>}
        {menuEntries.length > 0 && <MenuItem onClick={() => copyToClipboard()}><ListItemIcon><ContentCopyRounded /></ListItemIcon><ListItemText>{menuActionLabel('copy')}</ListItemText></MenuItem>}
        {menuEntries.length > 0 && <MenuItem onClick={() => { const targets = [...menuEntries]; closeMenu(); downloadEntries(targets) }}><ListItemIcon><DownloadRounded /></ListItemIcon><ListItemText>{menuActionLabel('download')}</ListItemText></MenuItem>}
        {menuEntries.length === 1 && selected && <MenuItem onClick={() => { setSharing(selected); closeMenu() }}><ListItemIcon><ShareIcon /></ListItemIcon><ListItemText>{t('files.share')}</ListItemText></MenuItem>}
        {menuEntries.length === 1 && selected?.type === 'file' && <MenuItem onClick={() => { void api.files.checksum(selected.path).then((result) => setNotice(`${result.algorithm}: ${result.value}`)).catch((error: unknown) => setNotice(error instanceof Error ? error.message : t('common.error'))); closeMenu() }}><ListItemIcon><FingerprintRounded /></ListItemIcon><ListItemText>{t('files.checksum')}</ListItemText></MenuItem>}
        {clipboard.length > 0 && selected?.type === 'directory' && <MenuItem disabled={!clipboard.every((entry) => canPasteInto(selected.path, entry))} onClick={() => pasteClipboard(selected.path)}><ListItemIcon><ContentPasteRounded /></ListItemIcon><ListItemText>{t('files.paste')}</ListItemText></MenuItem>}
        {menuEntries.length > 0 && <MenuItem sx={{ color: 'error.main' }} onClick={() => beginDelete()}><ListItemIcon><DeleteOutlineRounded color="error" /></ListItemIcon><ListItemText>{menuActionLabel('delete')}</ListItemText></MenuItem>}
      </Menu>

      <Dialog open={newFileOpen} onClose={() => setNewFileOpen(false)} maxWidth="sm"><DialogTitle>{t('files.newFile')}</DialogTitle><DialogContent sx={{ pt: 2, overflow: 'visible' }}><TextField fullWidth autoFocus label={t('files.fileName')} value={fileName} onChange={(event) => setFileName(event.target.value)} error={Boolean(createFile.error)} helperText={createFile.error instanceof Error ? createFile.error.message : ''} sx={{ minWidth: 0 }} /></DialogContent><DialogActions><Button onClick={() => setNewFileOpen(false)}>{t('common.cancel')}</Button><Button variant="contained" disabled={!fileName || fileName.includes('/') || createFile.isPending} onClick={() => createFile.mutate()}>{t('common.create')}</Button></DialogActions></Dialog>
      <Dialog open={newFolderOpen} onClose={() => setNewFolderOpen(false)} maxWidth="sm"><DialogTitle>{t('files.newFolder')}</DialogTitle><DialogContent sx={{ pt: 2, overflow: 'visible' }}><TextField fullWidth autoFocus label={t('files.folderName')} value={folderName} onChange={(event) => setFolderName(event.target.value)} error={Boolean(createFolder.error)} helperText={createFolder.error instanceof Error ? createFolder.error.message : ''} sx={{ minWidth: 0 }} /></DialogContent><DialogActions><Button onClick={() => setNewFolderOpen(false)}>{t('common.cancel')}</Button><Button variant="contained" disabled={!folderName || folderName.includes('/') || createFolder.isPending} onClick={() => createFolder.mutate()}>{t('common.create')}</Button></DialogActions></Dialog>
      <Dialog open={deleting.length > 0} onClose={() => setDeleting([])} maxWidth="xs"><DialogTitle>{deleting.length > 1 ? t('files.deleteItems', { count: deleting.length }) : `${t('files.delete')} ${deleting[0]?.name ?? ''}?`}</DialogTitle><DialogContent>{deleting.length > 1 && <Typography color="text.secondary" mb={1}>{t('files.itemsSelected', { count: deleting.length })}</Typography>}<Typography color="text.secondary">This cannot be undone.</Typography>{remove.error && <Box mt={2}><ErrorPane error={remove.error} /></Box>}</DialogContent><DialogActions><Button onClick={() => setDeleting([])}>{t('common.cancel')}</Button><Button color="error" variant="contained" disabled={remove.isPending} onClick={() => deleting.length > 0 && remove.mutate(deleting)}>{t('files.delete')}</Button></DialogActions></Dialog>
      <Dialog open={Boolean(droppedMove)} onClose={moveDroppedEntry.isPending ? undefined : () => { setDroppedMove(null); moveDroppedEntry.reset() }} maxWidth="xs"><DialogTitle>{t('files.move')}</DialogTitle><DialogContent><Typography><Trans i18nKey="files.confirmMove" values={{ name: droppedMove?.entry.name, destination: folderLabel(droppedMove?.destination ?? '') }} components={{ filename: <span style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace' }} />, path: <span style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace' }} /> }} /></Typography>{moveDroppedEntry.error && <Box mt={2}><ErrorPane error={moveDroppedEntry.error} /></Box>}</DialogContent><DialogActions><Button disabled={moveDroppedEntry.isPending} onClick={() => { setDroppedMove(null); moveDroppedEntry.reset() }}>{t('common.cancel')}</Button>{isConflictError(moveDroppedEntry.error) ? <Button color="warning" variant="contained" disabled={moveDroppedEntry.isPending} onClick={() => droppedMove && moveDroppedEntry.mutate({ ...droppedMove, overwrite: true })}>{t('files.replace')}</Button> : <Button variant="contained" disabled={moveDroppedEntry.isPending} onClick={() => droppedMove && moveDroppedEntry.mutate({ ...droppedMove, overwrite: false })}>{t('files.move')}</Button>}</DialogActions></Dialog>
      <Dialog open={Boolean(conflict)} onClose={() => resolveConflict('cancel')} maxWidth="xs"><DialogTitle>{t('files.conflictTitle')}</DialogTitle><DialogContent><Typography>{t('files.conflictBody', { name: conflict?.name })}</Typography></DialogContent><DialogActions><Button onClick={() => resolveConflict('cancel')}>{t('common.cancel')}</Button><Button onClick={() => resolveConflict('skip-all')}>{t('files.skipAll')}</Button><Button color="warning" variant="contained" onClick={() => resolveConflict('replace-all')}>{t('files.replaceAll')}</Button></DialogActions></Dialog>
      <FilePreviewDialog entry={preview} onClose={() => setPreview(null)} onEdit={() => { setEditor(preview); setPreview(null) }} />
      <FileEditorDialog entry={editor} onClose={() => setEditor(null)} onSaved={refresh} />
      <PathActionDialog action={pathAction?.action ?? null} entries={pathAction?.entries ?? []} copyDestination={pathAction?.copyDestination} onClose={() => setPathAction(null)} onDone={() => { setSelected(null); setSelectedPaths(new Set()); selectionAnchor.current = null; refresh() }} />
      <CreateShareDialog entry={sharing} onClose={() => setSharing(null)} onCreated={(url) => { if (url) void navigator.clipboard.writeText(publicShareUrl(url)); setNotice(url ? t('common.copied') : t('shares.create')) }} />
      <Snackbar open={Boolean(notice)} autoHideDuration={5000} onClose={() => setNotice('')} message={notice} />
    </Box>
  )
}
