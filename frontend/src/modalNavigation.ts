import { useEffect, useRef } from 'react'

type ModalRegistration = {
  id: symbol
  historyIndex: number | null
  close: () => void
}

const registrations: ModalRegistration[] = []
let installed = false
let restoringIndex: number | null = null

function stateIndex(state: unknown) {
  if (typeof state !== 'object' || state === null || !('idx' in state)) return null
  const index = (state as { idx?: unknown }).idx
  return typeof index === 'number' ? index : null
}

function handleHistoryNavigation(event: PopStateEvent) {
  const targetIndex = stateIndex(event.state)
  if (restoringIndex !== null && targetIndex === restoringIndex) {
    event.stopImmediatePropagation()
    restoringIndex = null
    return
  }

  const active = registrations.at(-1)
  if (!active) return

  event.stopImmediatePropagation()
  if (active.historyIndex !== null && targetIndex !== null && active.historyIndex !== targetIndex) {
    restoringIndex = active.historyIndex
    window.history.go(active.historyIndex - targetIndex)
  }
  active.close()
}

export function installModalNavigationGuard() {
  if (installed || typeof window === 'undefined') return
  installed = true
  window.addEventListener('popstate', handleHistoryNavigation, true)
}

export function useCloseOnHistoryNavigation(open: boolean, onClose: () => void) {
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  useEffect(() => {
    if (!open) return
    const registration: ModalRegistration = {
      id: Symbol('modal'),
      historyIndex: restoringIndex ?? stateIndex(window.history.state),
      close: () => closeRef.current(),
    }
    registrations.push(registration)
    return () => {
      const index = registrations.findIndex((candidate) => candidate.id === registration.id)
      if (index >= 0) registrations.splice(index, 1)
    }
  }, [open])
}
