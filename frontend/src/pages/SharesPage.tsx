import { useState, type FormEvent } from 'react'
import {
  Alert, Box, Button, Card, CardContent, Chip, Dialog, DialogActions, DialogContent,
  DialogTitle, IconButton, MenuItem, Snackbar, Stack, TextField, Tooltip, Typography,
} from '@mui/material'
import AddLinkRounded from '@mui/icons-material/AddLinkRounded'
import ContentCopyRounded from '@mui/icons-material/ContentCopyRounded'
import DeleteOutlineRounded from '@mui/icons-material/DeleteOutlineRounded'
import LockRounded from '@mui/icons-material/LockRounded'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import { formatDate, publicShareUrl } from '../utils'
import { ErrorPane, LoadingPane } from '../components/Feedback'
import { PageHeader } from '../components/PageHeader'

export function SharesPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const shares = useQuery({ queryKey: ['shares'], queryFn: api.shares.list })
  const [open, setOpen] = useState(false)
  const [path, setPath] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [expiry, setExpiry] = useState(86_400)
  const [notice, setNotice] = useState('')
  const [createdUrl, setCreatedUrl] = useState('')

  const create = useMutation({
    mutationFn: () => api.shares.create({ path, name: name || undefined, password: password || undefined, expiresInSeconds: expiry || undefined }),
    onSuccess: (share) => {
      setOpen(false); setPath(''); setName(''); setPassword('')
      if (share.url) setCreatedUrl(publicShareUrl(share.url))
      void queryClient.invalidateQueries({ queryKey: ['shares'] })
    },
  })
  const revoke = useMutation({
    mutationFn: api.shares.remove,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['shares'] }),
  })

  const submit = (event: FormEvent) => { event.preventDefault(); create.mutate() }
  const copy = async (url?: string) => {
    if (!url) return
    await navigator.clipboard.writeText(publicShareUrl(url))
    setNotice(t('common.copied'))
  }

  return (
    <Stack gap={2.5}>
      <PageHeader title={t('shares.title')} actions={<Button variant="contained" startIcon={<AddLinkRounded />} onClick={() => setOpen(true)}>{t('shares.create')}</Button>}>
        <Typography color="text.secondary">{t('shares.intro')}</Typography>
      </PageHeader>
        {shares.isPending ? <LoadingPane /> : shares.error ? <ErrorPane error={shares.error} retry={() => void shares.refetch()} /> : shares.data.length === 0 ? (
          <Card variant="outlined"><CardContent sx={{ textAlign: 'center', py: 8 }}><AddLinkRounded sx={{ fontSize: 44, color: 'text.disabled' }} /><Typography variant="h2" mt={1}>{t('shares.empty')}</Typography></CardContent></Card>
        ) : (
          <Stack gap={1.25}>{shares.data.map((share) => (
            <Card key={share.id} variant="outlined"><CardContent><Stack direction={{ xs: 'column', sm: 'row' }} gap={2} alignItems={{ sm: 'center' }}>
              <Box flex={1} minWidth={0}><Stack direction="row" alignItems="center" gap={1}><Typography fontWeight={650} className="file-name">{share.name || share.path}</Typography>{share.passwordProtected && <Chip icon={<LockRounded />} size="small" label={t('shares.protected')} />}</Stack><Typography color="text.secondary" variant="body2" className="file-name">{share.path}</Typography><Typography color="text.secondary" variant="caption">{share.expiresAt ? `Expires ${formatDate(share.expiresAt)}` : t('shares.never')}</Typography></Box>
              <Stack direction="row"><Tooltip title={t('shares.copy')}><span><IconButton disabled={!share.url} onClick={() => void copy(share.url)}><ContentCopyRounded /></IconButton></span></Tooltip><Tooltip title={t('shares.revoke')}><IconButton color="error" onClick={() => revoke.mutate(share.id)}><DeleteOutlineRounded /></IconButton></Tooltip></Stack>
            </Stack></CardContent></Card>
          ))}</Stack>
        )}

      <Dialog open={open} onClose={() => setOpen(false)} maxWidth="xs"><Stack component="form" onSubmit={submit}><DialogTitle>{t('shares.create')}</DialogTitle><DialogContent sx={{ pt: 2, overflow: 'visible' }}><Stack gap={2} pt={0.5}>
        <TextField required autoFocus label={t('shares.path')} placeholder="/Books" value={path} onChange={(event) => setPath(event.target.value)} />
        <TextField label={t('shares.name')} value={name} onChange={(event) => setName(event.target.value)} />
        <TextField label={t('shares.password')} type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
        <TextField select label={t('shares.expiry')} value={expiry} onChange={(event) => setExpiry(Number(event.target.value))}><MenuItem value={3600}>{t('shares.oneHour')}</MenuItem><MenuItem value={86400}>{t('shares.oneDay')}</MenuItem><MenuItem value={604800}>{t('shares.oneWeek')}</MenuItem><MenuItem value={0}>{t('shares.never')}</MenuItem></TextField>
        {create.error && <ErrorPane error={create.error} />}
      </Stack></DialogContent><DialogActions><Button onClick={() => setOpen(false)}>{t('common.cancel')}</Button><Button type="submit" variant="contained" disabled={!path.startsWith('/') || create.isPending}>{t('common.create')}</Button></DialogActions></Stack></Dialog>
      <Dialog open={Boolean(createdUrl)} onClose={() => setCreatedUrl('')} maxWidth="sm"><DialogTitle>{t('shares.create')}</DialogTitle><DialogContent><Alert severity="info" sx={{ mb: 2 }}>Copy this capability link now. ZenFM may not show its secret again.</Alert><TextField fullWidth value={createdUrl} InputProps={{ readOnly: true }} /></DialogContent><DialogActions><Button onClick={() => void copy(createdUrl)} startIcon={<ContentCopyRounded />}>{t('shares.copy')}</Button><Button onClick={() => setCreatedUrl('')}>{t('common.close')}</Button></DialogActions></Dialog>
      <Snackbar open={Boolean(notice)} autoHideDuration={3500} onClose={() => setNotice('')} message={notice} />
    </Stack>
  )
}
