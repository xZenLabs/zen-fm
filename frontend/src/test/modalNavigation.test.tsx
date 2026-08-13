import { act, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { installModalNavigationGuard, useCloseOnHistoryNavigation } from '../modalNavigation'

installModalNavigationGuard()

function OpenModal({ onClose }: { onClose: () => void }) {
  useCloseOnHistoryNavigation(true, onClose)
  return <div role="dialog">Open dialog</div>
}

function ConfirmingModal() {
  const [confirming, setConfirming] = useState(false)
  useCloseOnHistoryNavigation(true, () => setConfirming(true))
  useCloseOnHistoryNavigation(confirming, () => setConfirming(false))
  return <>{confirming && <div role="dialog">Confirm close</div>}</>
}

it.each([
  ['back', 9, 1],
  ['forward', 11, -1],
] as const)('closes an open modal on browser %s without notifying the router', (_direction, targetIndex, restorationDelta) => {
  window.history.replaceState({ idx: 10 }, '', window.location.href)
  const go = vi.spyOn(window.history, 'go').mockImplementation(() => undefined)
  const routerListener = vi.fn()
  const onClose = vi.fn()
  window.addEventListener('popstate', routerListener)
  const view = render(<OpenModal onClose={onClose} />)

  void act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: targetIndex } })) })

  expect(onClose).toHaveBeenCalledOnce()
  expect(go).toHaveBeenCalledWith(restorationDelta)
  expect(routerListener).not.toHaveBeenCalled()

  void act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 10 } })) })
  expect(routerListener).not.toHaveBeenCalled()
  view.unmount()
  window.removeEventListener('popstate', routerListener)
})

it('keeps a confirmation opened during restoration on the current history entry', () => {
  window.history.replaceState({ idx: 30 }, '', window.location.href)
  const go = vi.spyOn(window.history, 'go').mockImplementation(() => undefined)
  const view = render(<ConfirmingModal />)

  void act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 29 } })) })
  expect(screen.getByRole('dialog')).toHaveTextContent('Confirm close')
  void act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 30 } })) })

  go.mockClear()
  void act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 29 } })) })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(go).toHaveBeenCalledWith(1)
  void act(() => { window.dispatchEvent(new PopStateEvent('popstate', { state: { idx: 30 } })) })
  view.unmount()
})
