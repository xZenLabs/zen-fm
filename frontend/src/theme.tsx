import { createContext, useContext, useEffect, useMemo, useState, type KeyboardEvent, type PropsWithChildren } from 'react'
import { CssBaseline, ThemeProvider, createTheme, useMediaQuery } from '@mui/material'
import type { ThemePreference } from './api/types'

interface ThemeModeValue {
  preference: ThemePreference
  setPreference: (preference: ThemePreference) => void
}

const ThemeModeContext = createContext<ThemeModeValue>({ preference: 'system', setPreference: () => undefined })

function activateDialogPrimaryAction(event: KeyboardEvent<HTMLElement>) {
  if (event.key !== 'Enter' || event.nativeEvent.isComposing) return
  const target = event.target
  if (target instanceof HTMLElement && target.closest('.cm-content[contenteditable="true"]')) return
  const primaryAction = event.currentTarget.querySelector<HTMLElement>('.MuiDialogActions-root .MuiButton-contained:not(.Mui-disabled)')
  if (!primaryAction) return
  event.preventDefault()
  if (!event.repeat) primaryAction.click()
}

export function ZenThemeProvider({ children }: PropsWithChildren) {
  const systemDark = useMediaQuery('(prefers-color-scheme: dark)')
  const [preference, setPreference] = useState<ThemePreference>('system')
  const dark = preference === 'dark' || (preference === 'system' && systemDark)
  const accent = dark ? '#11b7a4' : '#0d9488'
  const buttonText = dark ? '#f8fafc' : '#111111'
  useEffect(() => {
    document.documentElement.dataset.zenfmTheme = dark ? 'dark' : 'light'
    return () => { delete document.documentElement.dataset.zenfmTheme }
  }, [dark])
  const theme = useMemo(() => createTheme({
    palette: {
      mode: dark ? 'dark' : 'light',
      primary: { main: accent },
      secondary: { main: accent },
      background: dark ? { default: '#0d1117', paper: '#161b22' } : { default: '#f5f5f1', paper: '#ffffff' },
      divider: dark ? 'rgba(148,163,184,.16)' : 'rgba(24,38,34,.09)',
    },
    shape: { borderRadius: 8 },
    typography: {
      fontFamily: 'Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
      h1: { fontSize: 'clamp(1.8rem, 4vw, 2.65rem)', fontWeight: 650, letterSpacing: '-0.035em' },
      h2: { fontSize: '1.4rem', fontWeight: 650, letterSpacing: '-0.02em' },
      button: { textTransform: 'none', fontWeight: 600 },
    },
    components: {
      MuiButton: {
        styleOverrides: {
          root: {
            minHeight: 44,
            boxShadow: 'none',
            '&:not(.MuiButton-contained)': { color: buttonText },
            '&:not(.MuiButton-contained) .MuiButton-startIcon, &:not(.MuiButton-contained) .MuiButton-endIcon': {
              color: accent,
            },
            '&:not(.MuiButton-contained).Mui-disabled .MuiButton-startIcon, &:not(.MuiButton-contained).Mui-disabled .MuiButton-endIcon': {
              color: dark ? 'rgba(255,255,255,.3)' : 'rgba(0,0,0,.26)',
            },
          },
        },
      },
      MuiIconButton: { styleOverrides: { root: { minWidth: 44, minHeight: 44 } } },
      MuiListItemIcon: { styleOverrides: { root: { color: accent } } },
      MuiFormControlLabel: { styleOverrides: { root: { gap: 10, marginLeft: 0, marginRight: 0 } } },
      MuiSwitch: {
        styleOverrides: {
          root: { width: 48, height: 24, padding: 0, overflow: 'visible' },
          switchBase: {
            padding: 3,
            transitionDuration: '180ms',
            '&.Mui-checked': {
              color: '#fff',
              transform: 'translateX(24px)',
              '& + .MuiSwitch-track': { backgroundColor: accent, opacity: 1 },
            },
          },
          thumb: { width: 18, height: 18, boxShadow: '0 1px 3px rgba(0,0,0,.35)' },
          track: { borderRadius: 12, backgroundColor: dark ? '#3a414d' : '#c7c7cc', opacity: 1 },
        },
      },
      MuiTextField: { defaultProps: { size: 'small' } },
      MuiPaper: { defaultProps: { elevation: 0 } },
      MuiDialog: { defaultProps: { fullWidth: true, onKeyDown: activateDialogPrimaryAction } },
    },
  }), [accent, buttonText, dark])

  const context = useMemo(() => ({ preference, setPreference }), [preference])
  return (
    <ThemeModeContext.Provider value={context}>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        {children}
      </ThemeProvider>
    </ThemeModeContext.Provider>
  )
}

export const useThemeMode = () => useContext(ThemeModeContext)
