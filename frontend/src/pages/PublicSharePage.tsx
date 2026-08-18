import { useState, type FormEvent } from 'react'
import { Alert, Box, Breadcrumbs, Button, Card, CardContent, Container, Link, Stack, Typography } from '@mui/material'
import DownloadIcon from '@mui/icons-material/Download'
import FolderRounded from '@mui/icons-material/FolderRounded'
import InsertDriveFileRounded from '@mui/icons-material/InsertDriveFileRounded'
import LockRounded from '@mui/icons-material/LockRounded'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link as RouterLink, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import { ZenMark } from '../components/ZenMark'
import { ErrorPane, LoadingPane } from '../components/Feedback'
import { PasswordField } from '../components/PasswordField'
import { formatBytes, formatDate } from '../utils'

function publicShareRoute(secret: string, path: string) {
  const suffix = path.split('/').filter(Boolean).map(encodeURIComponent).join('/')
  return `/s/${encodeURIComponent(secret)}${suffix ? `/${suffix}` : ''}`
}

export function PublicSharePage() {
  const { t } = useTranslation()
  const params = useParams()
  const secret = params.secret || new URLSearchParams(window.location.hash.slice(1)).get('secret') || ''
  const relativePath = `/${params['*'] ?? ''}`.replace(/\/{2,}/g, '/')
  const queryClient = useQueryClient()
  const [password, setPassword] = useState('')
  const share = useQuery({ queryKey: ['public-share', secret, relativePath], queryFn: () => api.shares.public(secret, undefined, relativePath), retry: false })
  const unlock = useMutation({
    mutationFn: () => api.shares.public(secret, password, relativePath),
    onSuccess: (data) => queryClient.setQueryData(['public-share', secret, relativePath], data),
  })
  const submit = (event: FormEvent) => { event.preventDefault(); unlock.mutate() }
  const currentPath = share.data?.path ?? relativePath
  const breadcrumbs = currentPath.split('/').filter(Boolean)

  return (
    <Box minHeight="100dvh">
      <Box component="header" borderBottom={1} borderColor="divider"><Container maxWidth="lg" sx={{ py: 2 }}><ZenMark /></Container></Box>
      <Container component="main" maxWidth="md" sx={{ py: { xs: 4, sm: 7 } }}>
        {share.isPending ? <LoadingPane /> : share.error ? <ErrorPane error={share.error} /> : share.data.passwordRequired ? (
          <Card variant="outlined" sx={{ maxWidth: 430, mx: 'auto' }}><CardContent><Stack component="form" gap={2.5} onSubmit={submit}><LockRounded color="primary" /><Box><Typography variant="h1">{t('shares.publicTitle')}</Typography><Typography color="text.secondary" mt={1}>Enter the password to continue.</Typography></Box><PasswordField autoFocus label={t('shares.password')} value={password} onChange={(event) => setPassword(event.target.value)} />{unlock.error && <Alert severity="error">{unlock.error.message}</Alert>}<Button type="submit" variant="contained" disabled={!password || unlock.isPending}>{t('shares.unlock')}</Button></Stack></CardContent></Card>
        ) : (
          <Stack gap={3}>
            <Box><Typography variant="h1">{share.data.name}</Typography><Typography color="text.secondary" mt={0.5}>{share.data.expiresAt ? `Available until ${formatDate(share.data.expiresAt)}` : t('shares.publicTitle')}</Typography></Box>
            {share.data.entries && <Breadcrumbs aria-label="Shared path">
              <Link component={RouterLink} to={publicShareRoute(secret, '/')} underline="hover" color={breadcrumbs.length === 0 ? 'text.primary' : 'inherit'}>{t('shares.root')}</Link>
              {breadcrumbs.map((part, index) => {
                const target = `/${breadcrumbs.slice(0, index + 1).join('/')}`
                return <Link key={target} component={RouterLink} to={publicShareRoute(secret, target)} underline="hover" color={index === breadcrumbs.length - 1 ? 'text.primary' : 'inherit'}>{part}</Link>
              })}
            </Breadcrumbs>}
            {share.data.entry && share.data.entry.type !== 'directory' && !share.data.entries ? <Card variant="outlined"><CardContent><Stack direction="row" alignItems="center" gap={2}><InsertDriveFileRounded color="primary" /><Box flex={1} minWidth={0}><Typography fontWeight={650} className="file-name">{share.data.entry.name}</Typography><Typography variant="caption" color="text.secondary">{formatBytes(share.data.entry.size)}</Typography></Box><Button component="a" href={api.shares.publicRawUrl(secret)} startIcon={<DownloadIcon />} variant="contained">{t('shares.download')}</Button></Stack></CardContent></Card> : (
              <Stack gap={1}>{share.data.entries?.map((entry) => <Card variant="outlined" key={entry.path}><CardContent><Stack direction="row" alignItems="center" gap={2}>{entry.type === 'directory' ? <FolderRounded color="primary" /> : <InsertDriveFileRounded color="action" />}<Box flex={1} minWidth={0}>{entry.type === 'directory' ? <Typography component={RouterLink} to={publicShareRoute(secret, entry.path)} color="inherit" fontWeight={600} className="file-name" sx={{ display: 'block', textDecoration: 'none' }}>{entry.name}</Typography> : <Typography fontWeight={600} className="file-name">{entry.name}</Typography>}<Typography variant="caption" color="text.secondary">{entry.type === 'directory' ? '—' : formatBytes(entry.size)}</Typography></Box>{entry.type === 'file' && <Button component="a" href={api.shares.publicRawUrl(secret, entry.path)} startIcon={<DownloadIcon />}>{t('shares.download')}</Button>}</Stack></CardContent></Card>)}</Stack>
            )}
          </Stack>
        )}
      </Container>
    </Box>
  )
}
