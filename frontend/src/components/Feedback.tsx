import { Alert, Box, Button, CircularProgress, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

export function LoadingPane() {
  const { t } = useTranslation()
  return <Box minHeight={240} display="grid" sx={{ placeItems: 'center' }}><Stack alignItems="center" gap={1.5}><CircularProgress size={28} /><Typography color="text.secondary">{t('common.loading')}</Typography></Stack></Box>
}

export function ErrorPane({ error, retry }: { error: unknown; retry?: () => void }) {
  const { t } = useTranslation()
  const message = error instanceof Error ? error.message : t('common.error')
  return <Alert severity="error" action={retry ? <Button color="inherit" onClick={retry}>Retry</Button> : undefined}>{message}</Alert>
}
