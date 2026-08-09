export type ThemePreference = 'light' | 'dark' | 'system'
export type SortField = 'name' | 'size' | 'modified'
export type SortDirection = 'asc' | 'desc'
export type FileKind = 'file' | 'directory' | 'symlink' | 'special'

export interface Session {
  authenticated: boolean
  username?: string
  setupRequired: boolean
  csrfToken?: string
  idleExpiresAt?: string
  absoluteExpiresAt?: string
}

export interface Settings {
  theme: ThemePreference
  locale: string
  showHidden: boolean
  clientTimeoutSeconds: number
  advancedMode: boolean
  root: string
  secureTransport: boolean
}

export interface FileEntry {
  name: string
  path: string
  type: FileKind
  size: number
  modifiedAt: string
  mimeType?: string
  hidden?: boolean
  writable?: boolean
}

export interface FileListing {
  path: string
  entries: FileEntry[]
  advancedMode: boolean
  disk?: {
    used: number
    total: number
  }
}

export interface SearchResult {
  entries: FileEntry[]
  truncated: boolean
}

export interface Share {
  id: string
  path: string
  name?: string
  url?: string
  expiresAt?: string
  passwordProtected: boolean
  createdAt: string
}

export interface PublicShare {
  name: string
  path: string
  entry?: FileEntry
  entries?: FileEntry[]
  passwordRequired?: boolean
  expiresAt?: string
}

export interface PersonalToken {
  id: string
  name: string
  createdAt: string
  expiresAt: string
  lastUsedAt?: string
}

export interface CreatedToken extends PersonalToken {
  token: string
}

export interface Checksum {
  algorithm: string
  value: string
}

export interface DiskUsage {
  used: number
  total: number
}

export interface ProblemDetails {
  type?: string
  title?: string
  status?: number
  detail?: string
  instance?: string
  errors?: Record<string, string[]>
}

export interface CreateShareInput {
  path: string
  name?: string
  password?: string
  expiresInSeconds?: number
}

export interface CreateTokenInput {
  name: string
  expiresInSeconds: number
}
