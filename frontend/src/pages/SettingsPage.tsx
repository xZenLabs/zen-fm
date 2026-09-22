import { useEffect, useState, type FormEvent } from 'react'
import {
  Alert, Box, Breadcrumbs, Button, Card, CardContent, Dialog, DialogActions, DialogContent, DialogTitle,
  FormControlLabel, Link, MenuItem, Snackbar, Stack, Switch, TextField, Typography,
} from '@mui/material'
import KeyRounded from '@mui/icons-material/KeyRounded'
import ContentCopyRounded from '@mui/icons-material/ContentCopyRounded'
import DeleteOutlineRounded from '@mui/icons-material/DeleteOutlineRounded'
import FolderRounded from '@mui/icons-material/FolderRounded'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api, setClientTimeout } from '../api/client'
import type { Settings, ThemePreference } from '../api/types'
import { supportedLocales } from '../i18n'
import { useThemeMode } from '../theme'
import { useAuth } from '../auth/AuthProvider'
import { formatDate } from '../utils'
import { ErrorPane, LoadingPane } from '../components/Feedback'
import { PageHeader } from '../components/PageHeader'
import { PasswordField } from '../components/PasswordField'
import { useCloseOnHistoryNavigation } from '../modalNavigation'

function fullPath(root: string, path: string) {
  return `${root.replace(/\/+$/, '')}${path === '/' ? '' : path}` || '/'
}

export function SettingsPage() {
  const { t, i18n } = useTranslation()
  const { setPreference } = useThemeMode()
  const { refresh } = useAuth()
  const queryClient = useQueryClient()
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings.get })
  const tokens = useQuery({ queryKey: ['tokens'], queryFn: api.tokens.list })
  const [form, setForm] = useState<Partial<Settings>>({})
  const [notice, setNotice] = useState('')
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [tokenName, setTokenName] = useState('')
  const [tokenExpiry, setTokenExpiry] = useState(2_592_000)
  const [createdToken, setCreatedToken] = useState('')
  const [startupDirectoryOpen, setStartupDirectoryOpen] = useState(false)
  const [startupDirectoryPath, setStartupDirectoryPath] = useState('/')
  useCloseOnHistoryNavigation(Boolean(createdToken), () => setCreatedToken(''))
  useCloseOnHistoryNavigation(startupDirectoryOpen, () => setStartupDirectoryOpen(false))

  const startupDirectoryFolders = useQuery({
    queryKey: ['files', startupDirectoryPath, form.showHidden ?? false],
    queryFn: () => api.files.list(startupDirectoryPath, form.showHidden ?? false),
    enabled: startupDirectoryOpen,
  })
  const startupFolders = startupDirectoryFolders.data?.entries.filter((entry) => entry.type === 'directory') ?? []

  useEffect(() => { if (settings.data) setForm(settings.data) }, [settings.data])

  const save = useMutation({
    mutationFn: () => api.settings.update({ theme: form.theme, locale: form.locale, showHidden: form.showHidden, clientTimeoutSeconds: form.clientTimeoutSeconds, startupDirectory: settings.data?.advancedMode ? undefined : form.startupDirectory }),
    onSuccess: (next) => {
      queryClient.setQueryData(['settings'], next)
      setPreference(next.theme); setClientTimeout(next.clientTimeoutSeconds); void i18n.changeLanguage(next.locale); setNotice(t('settings.saved', { lng: next.locale }))
    },
  })
  const changePassword = useMutation({
    mutationFn: () => api.owner.changePassword(currentPassword, newPassword),
    onSuccess: async () => { setCurrentPassword(''); setNewPassword(''); await refresh(); setNotice(t('settings.passwordChanged')); void queryClient.invalidateQueries({ queryKey: ['tokens'] }) },
  })
  const createToken = useMutation({
    mutationFn: () => api.tokens.create({ name: tokenName, expiresInSeconds: tokenExpiry }),
    onSuccess: (created) => { setCreatedToken(created.token); setTokenName(''); void queryClient.invalidateQueries({ queryKey: ['tokens'] }) },
  })
  const revokeToken = useMutation({ mutationFn: api.tokens.remove, onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['tokens'] }) })

  const passwordSubmit = (event: FormEvent) => { event.preventDefault(); if (Array.from(newPassword).length >= 7) changePassword.mutate() }
  const chooseStartupDirectory = () => { setStartupDirectoryPath(form.startupDirectory ?? '/'); setStartupDirectoryOpen(true) }

  if (settings.isPending) return <Stack gap={2.5}><PageHeader title={t('settings.title')} /><LoadingPane /></Stack>
  if (settings.error) return <Stack gap={2.5}><PageHeader title={t('settings.title')} /><ErrorPane error={settings.error} /></Stack>
  const startupDirectoryFullPath = fullPath(settings.data.root, form.startupDirectory ?? '/')
  const startupRootName = settings.data.root.replace(/\/+$/, '').split('/').pop() || '/'
  const startupDirectoryBreadcrumbs = startupDirectoryPath.split('/').filter(Boolean)

  return (
    <Stack gap={2.5}>
      <PageHeader title={t('settings.title')} />
        <Card variant="outlined"><CardContent><Stack gap={2.5}>
          <Box><Typography variant="h2">{t('settings.general')}</Typography><Typography variant="body2" color="text.secondary">{t('settings.version')}: {settings.data.version}</Typography><Typography variant="body2" color="text.secondary">{t('settings.root')}: {settings.data.root}</Typography></Box>
          <Stack direction={{ xs: 'column', sm: 'row' }} gap={2}>
            <TextField select fullWidth label={t('settings.theme')} value={form.theme ?? 'system'} onChange={(event) => setForm((old) => ({ ...old, theme: event.target.value as ThemePreference }))}><MenuItem value="system">{t('settings.system')}</MenuItem><MenuItem value="light">{t('settings.light')}</MenuItem><MenuItem value="dark">{t('settings.dark')}</MenuItem></TextField>
            <TextField select fullWidth label={t('settings.language')} value={form.locale ?? 'en'} onChange={(event) => setForm((old) => ({ ...old, locale: event.target.value }))}>{supportedLocales.map((locale) => <MenuItem key={locale} value={locale}>{new Intl.DisplayNames([i18n.language], { type: 'language' }).of(locale) ?? locale}</MenuItem>)}</TextField>
          </Stack>
          <FormControlLabel control={<Switch checked={form.showHidden ?? false} onChange={(event) => setForm((old) => ({ ...old, showHidden: event.target.checked }))} />} label={t('settings.showHidden')} />
          <Box><Typography variant="body2" color="text.secondary" mb={0.5}>{t('settings.startupDirectory')}</Typography><Button fullWidth variant="outlined" color="inherit" startIcon={<FolderRounded color="primary" />} disabled={settings.data.advancedMode} aria-label={t('settings.chooseStartupDirectory')} onClick={chooseStartupDirectory} sx={{ justifyContent: 'flex-start', textTransform: 'none' }}><Box component="span" className="file-name">{startupDirectoryFullPath}</Box></Button><Typography variant="caption" color="text.secondary">{t('settings.startupDirectoryHint')}</Typography></Box>
          <TextField type="number" label={t('settings.timeout')} value={form.clientTimeoutSeconds ?? 30} onChange={(event) => setForm((old) => ({ ...old, clientTimeoutSeconds: Number(event.target.value) }))} inputProps={{ min: 0, max: 86400 }} helperText={t('settings.timeoutHint')} sx={{ maxWidth: 280 }} />
          {save.error && <ErrorPane error={save.error} />}<Button variant="contained" onClick={() => save.mutate()} disabled={save.isPending} sx={{ alignSelf: 'flex-start' }}>{t('settings.save')}</Button>
        </Stack></CardContent></Card>

        <Card variant="outlined"><CardContent><Stack component="form" gap={2} onSubmit={passwordSubmit}>
          <Typography variant="h2">{t('settings.password')}</Typography>
          <Stack direction={{ xs: 'column', sm: 'row' }} gap={2}><PasswordField fullWidth autoComplete="current-password" label={t('settings.currentPassword')} value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /><PasswordField fullWidth autoComplete="new-password" label={t('settings.newPassword')} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} helperText={t('settings.minimumPasswordHint')} /></Stack>
          {changePassword.error && <ErrorPane error={changePassword.error} />}<Button type="submit" variant="outlined" disabled={!currentPassword || Array.from(newPassword).length < 7 || changePassword.isPending} sx={{ alignSelf: 'flex-start' }}>{t('settings.changePassword')}</Button>
        </Stack></CardContent></Card>

        <Card variant="outlined"><CardContent><Stack gap={2}>
          <Box><Typography variant="h2">{t('settings.tokens')}</Typography><Typography color="text.secondary">{t('settings.tokenHint')}</Typography></Box>
          <Stack direction={{ xs: 'column', sm: 'row' }} gap={2}><TextField label={t('settings.tokenName')} value={tokenName} onChange={(event) => setTokenName(event.target.value)} sx={{ flex: 1 }} /><TextField select label={t('settings.lifetime')} value={tokenExpiry} onChange={(event) => setTokenExpiry(Number(event.target.value))} sx={{ minWidth: 150 }}><MenuItem value={86400}>{t('settings.oneDay')}</MenuItem><MenuItem value={2592000}>{t('settings.thirtyDays')}</MenuItem><MenuItem value={31536000}>{t('settings.oneYear')}</MenuItem></TextField><Button variant="contained" startIcon={<KeyRounded />} disabled={!tokenName || createToken.isPending} onClick={() => createToken.mutate()}>{t('settings.createToken')}</Button></Stack>
          {createToken.error && <ErrorPane error={createToken.error} />}
          {tokens.isPending ? <LoadingPane /> : tokens.error ? <ErrorPane error={tokens.error} /> : tokens.data.map((token) => <Stack key={token.id} direction="row" alignItems="center" gap={2} py={1} borderTop={1} borderColor="divider"><Box flex={1}><Typography fontWeight={600}>{token.name}</Typography><Typography variant="caption" color="text.secondary">{t('settings.expires')} {formatDate(token.expiresAt)}</Typography></Box><Button color="error" startIcon={<DeleteOutlineRounded color="error" />} onClick={() => revokeToken.mutate(token.id)}>{t('settings.revokeToken')}</Button></Stack>)}
        </Stack></CardContent></Card>
      <Dialog open={startupDirectoryOpen} onClose={() => setStartupDirectoryOpen(false)} maxWidth="xs" fullWidth>
        <DialogTitle>{t('settings.chooseStartupDirectory')}</DialogTitle>
        <DialogContent dividers sx={{ p: 0 }}><Stack>
          <Box px={2} py={1.5} bgcolor="action.hover" borderBottom={1} borderColor="divider"><Stack direction="row" alignItems="center" gap={1.5}><FolderRounded color="primary" fontSize="large" sx={{ flexShrink: 0 }} /><Box minWidth={0}><Typography variant="caption" color="text.secondary">{t('settings.currentFolder')}</Typography><Stack direction="row" alignItems="center" minWidth={0}><Typography component="span" aria-hidden="true" color="text.secondary" mr={0.75} fontWeight={600} fontFamily="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace">/</Typography><Breadcrumbs aria-label={t('settings.currentFolder')} sx={{ overflowX: 'auto', '& .MuiBreadcrumbs-ol': { flexWrap: 'nowrap' } }}><Link component="button" title={fullPath(settings.data.root, '/')} underline="hover" color={startupDirectoryBreadcrumbs.length === 0 ? 'text.primary' : 'inherit'} onClick={() => setStartupDirectoryPath('/')} sx={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace', fontWeight: 600, whiteSpace: 'nowrap' }}>{startupRootName}</Link>{startupDirectoryBreadcrumbs.map((part, index) => { const target = `/${startupDirectoryBreadcrumbs.slice(0, index + 1).join('/')}`; return <Link component="button" key={target} underline="hover" color={index === startupDirectoryBreadcrumbs.length - 1 ? 'text.primary' : 'inherit'} onClick={() => setStartupDirectoryPath(target)} sx={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace', fontWeight: 600, whiteSpace: 'nowrap' }}>{part}</Link> })}</Breadcrumbs></Stack></Box></Stack></Box>
          {startupDirectoryFolders.isPending ? <LoadingPane /> : startupDirectoryFolders.error ? <Box p={2}><ErrorPane error={startupDirectoryFolders.error} /></Box> : startupFolders.length === 0 ? <Typography color="text.secondary" p={2}>{t('files.empty')}</Typography> : startupFolders.map((folder) => <Button key={folder.path} fullWidth color="inherit" startIcon={<FolderRounded color="primary" />} onClick={() => setStartupDirectoryPath(folder.path)} sx={{ justifyContent: 'flex-start', borderRadius: 0 }}>{folder.name}</Button>)}
        </Stack></DialogContent>
        <DialogActions><Button onClick={() => setStartupDirectoryOpen(false)}>{t('common.cancel')}</Button><Button variant="contained" onClick={() => { setForm((old) => ({ ...old, startupDirectory: startupDirectoryPath })); setStartupDirectoryOpen(false) }}>{t('settings.selectStartupDirectory')}</Button></DialogActions>
      </Dialog>
      <Dialog open={Boolean(createdToken)} onClose={() => setCreatedToken('')} maxWidth="sm"><DialogTitle>{t('settings.tokenDialogTitle')}</DialogTitle><DialogContent><Alert severity="warning" sx={{ mb: 2 }}>{t('settings.tokenDialogWarning')}</Alert><TextField fullWidth value={createdToken} InputProps={{ readOnly: true }} /></DialogContent><DialogActions><Button startIcon={<ContentCopyRounded />} onClick={() => void navigator.clipboard.writeText(createdToken).then(() => setNotice(t('common.copied')))}>{t('settings.copyToken')}</Button><Button onClick={() => setCreatedToken('')}>{t('common.close')}</Button></DialogActions></Dialog>
      <Snackbar open={Boolean(notice)} autoHideDuration={4500} onClose={() => setNotice('')} message={notice} />
    </Stack>
  )
}
