import { createContext, useCallback, useContext, useEffect, useMemo, useState, type PropsWithChildren } from 'react'
import { ApiError, api, onUnauthorized } from '../api/client'
import type { Session } from '../api/types'

type AuthStatus = 'checking' | 'anonymous' | 'authenticated'

interface AuthContextValue {
  status: AuthStatus
  session: Session | null
  validationError: Error | null
  login: (username: string, password: string) => Promise<Session>
  logout: () => Promise<void>
  refresh: () => Promise<void>
  completeSetup: (newPassword: string) => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: PropsWithChildren) {
  const [status, setStatus] = useState<AuthStatus>('checking')
  const [session, setSession] = useState<Session | null>(null)
  const [validationError, setValidationError] = useState<Error | null>(null)

  const becomeAnonymous = useCallback(() => {
    setValidationError(null)
    setSession(null)
    setStatus('anonymous')
  }, [])

  const refresh = useCallback(async () => {
    try {
      const next = await api.session.get()
      if (!next.authenticated) return becomeAnonymous()
      setValidationError(null)
      setSession(next)
      setStatus('authenticated')
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) return becomeAnonymous()
      setValidationError(error instanceof Error ? error : new Error('Unable to validate the session.'))
      throw error
    }
  }, [becomeAnonymous])

  useEffect(() => {
    void refresh().catch(() => undefined)
    return onUnauthorized(becomeAnonymous)
  }, [becomeAnonymous, refresh])

  useEffect(() => {
    const validateOnResume = () => {
      if (document.visibilityState === 'visible' && status === 'authenticated') {
        void refresh().catch(() => undefined)
      }
    }
    document.addEventListener('visibilitychange', validateOnResume)
    return () => document.removeEventListener('visibilitychange', validateOnResume)
  }, [refresh, status])

  useEffect(() => {
    if (status !== 'authenticated' || !session) return
    const idleDeadline = session.idleExpiresAt ? Date.parse(session.idleExpiresAt) : Number.NaN
    const absoluteDeadline = session.absoluteExpiresAt ? Date.parse(session.absoluteExpiresAt) : Number.NaN
    const timers: number[] = []
    const schedule = (deadline: number, callback: () => void) => {
      if (!Number.isFinite(deadline)) return
      const tick = () => {
        const remaining = deadline - Date.now()
        if (remaining <= 0) callback()
        else timers.push(window.setTimeout(tick, Math.min(remaining, 2_147_483_647)))
      }
      tick()
    }
    schedule(absoluteDeadline, becomeAnonymous)
    if (!Number.isFinite(absoluteDeadline) || idleDeadline < absoluteDeadline) {
      schedule(idleDeadline, () => void refresh().catch(() => undefined))
    }
    return () => timers.forEach((timer) => window.clearTimeout(timer))
  }, [becomeAnonymous, refresh, session, status])

  const login = useCallback(async (username: string, password: string) => {
    const next = await api.session.login(username, password)
    setValidationError(null)
    setSession(next)
    setStatus(next.authenticated ? 'authenticated' : 'anonymous')
    return next
  }, [])

  const logout = useCallback(async () => {
    try { await api.session.logout() } finally { becomeAnonymous() }
  }, [becomeAnonymous])

  const completeSetup = useCallback(async (newPassword: string) => {
    const next = await api.owner.changePassword(undefined, newPassword)
    setValidationError(null)
    setSession({ ...next, authenticated: true, setupRequired: false })
    setStatus('authenticated')
  }, [])

  const value = useMemo(() => ({ status, session, validationError, login, logout, refresh, completeSetup }), [completeSetup, login, logout, refresh, session, status, validationError])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const context = useContext(AuthContext)
  if (!context) throw new Error('useAuth must be used within AuthProvider')
  return context
}
