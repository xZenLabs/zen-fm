import type { Location } from 'react-router-dom'
import { filesRoute } from '../utils'

interface ReturnLocationState {
  returnTo?: unknown
}

function privateLocation(value: unknown): string | null {
  if (typeof value !== 'string') return null
  if (value === '/files' || value.startsWith('/files/') || value.startsWith('/files?')) return value
  if (value === '/shares' || value === '/settings') return value
  return null
}

export function currentPrivateLocation(location: Location): string | null {
  return privateLocation(`${location.pathname}${location.search}${location.hash}`)
}

export function postAuthenticationLocation(state: unknown, defaultDirectory?: string): string {
  const returnTo = typeof state === 'object' && state !== null
    ? privateLocation((state as ReturnLocationState).returnTo)
    : null
  return returnTo ?? filesRoute(defaultDirectory || '/')
}
