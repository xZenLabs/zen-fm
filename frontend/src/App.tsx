import { lazy, Suspense } from 'react'
import { Alert, Box, Button, CircularProgress } from '@mui/material'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useAuth } from './auth/AuthProvider'
import { currentPrivateLocation, postAuthenticationLocation } from './auth/navigation'
import { AppShell } from './components/AppShell'

const LoginPage = lazy(() => import('./pages/LoginPage').then((module) => ({ default: module.LoginPage })))
const SetupPage = lazy(() => import('./pages/SetupPage').then((module) => ({ default: module.SetupPage })))
const FilesPage = lazy(() => import('./pages/FilesPage').then((module) => ({ default: module.FilesPage })))
const SharesPage = lazy(() => import('./pages/SharesPage').then((module) => ({ default: module.SharesPage })))
const SettingsPage = lazy(() => import('./pages/SettingsPage').then((module) => ({ default: module.SettingsPage })))
const PublicSharePage = lazy(() => import('./pages/PublicSharePage').then((module) => ({ default: module.PublicSharePage })))

function Loading() {
  return <Box minHeight="100dvh" display="grid" sx={{ placeItems: 'center' }}><CircularProgress size={30} aria-label="Loading" /></Box>
}

export default function App() {
  const { status, session, validationError, refresh } = useAuth()
  const location = useLocation()

  if (status === 'checking') {
    if (!validationError) return <Loading />
    return <Box minHeight="100dvh" display="grid" p={2} sx={{ placeItems: 'center' }}><Alert severity="warning" action={<Button color="inherit" onClick={() => void refresh().catch(() => undefined)}>Retry</Button>}>ZenFM could not reach the server. Your browser session has not been discarded.</Alert></Box>
  }

  return (
    <><Box aria-live="polite" position="fixed" top={8} left="50%" zIndex="tooltip" sx={{ transform: 'translateX(-50%)', width: 'min(92vw, 720px)' }}>{validationError && <Alert severity="warning" action={<Button color="inherit" onClick={() => void refresh().catch(() => undefined)}>Reconnect</Button>}>Server connection lost. Your session is retained while ZenFM reconnects.</Alert>}</Box>
    <Suspense fallback={<Loading />}><Routes>
      <Route path="/s/:secret/*" element={<PublicSharePage />} />
      <Route path="/share/:shareId/*" element={<PublicSharePage />} />
      {status === 'anonymous' ? (
        <>
          <Route path="/login" element={<LoginPage />} />
          <Route path="*" element={<Navigate to="/login" replace state={{ returnTo: currentPrivateLocation(location) }} />} />
        </>
      ) : session?.setupRequired ? (
        <>
          <Route path="/setup" element={<SetupPage />} />
          <Route path="*" element={<Navigate to="/setup" replace state={{
            returnTo: currentPrivateLocation(location) ?? postAuthenticationLocation(location.state, session.defaultDirectory),
          }} />} />
        </>
      ) : (
        <Route element={<AppShell />}>
          <Route path="/files/*" element={<FilesPage />} />
          <Route path="/shares" element={<SharesPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to={postAuthenticationLocation(location.state, session?.defaultDirectory)} replace />} />
        </Route>
      )}
    </Routes></Suspense></>
  )
}
