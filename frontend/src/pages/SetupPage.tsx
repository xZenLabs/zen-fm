import { useState, type FormEvent } from 'react'
import { Alert, Button, LinearProgress, Stack, TextField } from '@mui/material'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/AuthProvider'
import { AuthLayout } from '../components/AuthLayout'

export function SetupPage() {
  const { t } = useTranslation()
  const { completeSetup } = useAuth()
  const navigate = useNavigate()
  const [currentPassword, setCurrentPassword] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const passwordValid = Array.from(password).length >= 12 && password === confirmation

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!passwordValid) return
    setPending(true)
    setError('')
    try {
      await completeSetup(currentPassword, password)
      void navigate('/files', { replace: true })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('common.error'))
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthLayout title={t('auth.setupTitle')} subtitle={t('auth.setupBody')}>
      <Stack component="form" gap={2} onSubmit={(event) => void submit(event)}>
        {pending && <LinearProgress />}
        {error && <Alert severity="error">{error}</Alert>}
        <TextField label={t('auth.currentPassword')} type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} required />
        <TextField label={t('auth.newPassword')} type="password" autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} helperText={t('auth.passwordHint')} required />
        <TextField label={t('auth.confirmPassword')} type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} error={Boolean(confirmation && confirmation !== password)} required />
        <Button variant="contained" size="large" type="submit" disabled={pending || !currentPassword || !passwordValid}>{t('auth.completeSetup')}</Button>
      </Stack>
    </AuthLayout>
  )
}
