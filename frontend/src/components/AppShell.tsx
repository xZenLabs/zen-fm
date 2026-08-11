import { useEffect } from 'react'
import { Alert, AppBar, Box, Button, Container, Stack, Toolbar, useMediaQuery, useTheme } from '@mui/material'
import FolderRounded from '@mui/icons-material/FolderRounded'
import LinkRounded from '@mui/icons-material/LinkRounded'
import SettingsRounded from '@mui/icons-material/SettingsRounded'
import LogoutRounded from '@mui/icons-material/LogoutRounded'
import { NavLink, Outlet } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, setClientTimeout } from '../api/client'
import { useAuth } from '../auth/AuthProvider'
import { useThemeMode } from '../theme'
import { ZenMark } from './ZenMark'

const nav = [
  { to: '/files', label: 'nav.files', icon: <FolderRounded fontSize="small" /> },
  { to: '/shares', label: 'nav.shares', icon: <LinkRounded fontSize="small" /> },
  { to: '/settings', label: 'nav.settings', icon: <SettingsRounded fontSize="small" /> },
] as const

export function AppShell() {
  const { t, i18n } = useTranslation()
  const { logout } = useAuth()
  const { setPreference } = useThemeMode()
  const theme = useTheme()
  const mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings.get })

  useEffect(() => {
    if (!settings.data) return
    setPreference(settings.data.theme)
    setClientTimeout(settings.data.clientTimeoutSeconds)
    void i18n.changeLanguage(settings.data.locale)
  }, [i18n, setPreference, settings.data])

  const insecure = window.location.protocol === 'http:'

  return (
    <Box className="app-shell" minHeight="100dvh" pb={mobile ? 10 : 0}>
      <AppBar position="sticky" color="transparent" sx={{ backdropFilter: 'blur(18px)', borderBottom: 1, borderColor: 'divider', backgroundImage: 'none' }}>
        <Container maxWidth="xl">
          <Toolbar disableGutters sx={{ gap: 2 }}>
            <ZenMark />
            {!mobile && (
              <Stack direction="row" gap={0.5} ml={3} flex={1}>
                {nav.map((item) => <Button key={item.to} component={NavLink} to={item.to} startIcon={item.icon} className="nav-button">{t(item.label)}</Button>)}
              </Stack>
            )}
            <Button onClick={() => void logout()} color="inherit" startIcon={<LogoutRounded />} aria-label={t('nav.logout')} sx={{ ml: 'auto' }}>{mobile ? '' : t('nav.logout')}</Button>
          </Toolbar>
        </Container>
      </AppBar>
      {(insecure || settings.data?.advancedMode) && (
        <Container maxWidth="xl" sx={{ pt: 2 }}>
          <Stack gap={1}>
            {insecure && <Alert severity="warning" variant="outlined">{t('warning.http')}</Alert>}
            {settings.data?.advancedMode && <Alert severity="error" variant="outlined">{t('warning.advanced')}</Alert>}
          </Stack>
        </Container>
      )}
      <Container component="main" maxWidth="xl" sx={{ py: { xs: 2.5, sm: 4 }, minHeight: 'calc(100dvh - 64px)', display: 'flex', flexDirection: 'column' }}>
        <Outlet />
      </Container>
      {mobile && (
        <Box component="nav" className="bottom-nav" aria-label="Primary navigation">
          {nav.map((item) => (
            <Button key={item.to} component={NavLink} to={item.to} color="inherit" className="bottom-nav-button">
              {item.icon}<span>{t(item.label)}</span>
            </Button>
          ))}
        </Box>
      )}
    </Box>
  )
}
