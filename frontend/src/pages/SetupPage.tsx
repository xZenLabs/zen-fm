import { useState, type FormEvent } from 'react'
import { Alert, Button, LinearProgress, Stack } from '@mui/material'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/AuthProvider'
import { offerToSavePassword } from '../auth/passwordCredential'
import { AuthLayout } from '../components/AuthLayout'
import { PasswordField } from '../components/PasswordField'

export function SetupPage() {
  const { t } = useTranslation()
  const { completeSetup, session } = useAuth()
  const navigate = useNavigate()
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
      await completeSetup(password)
      await offerToSavePassword(session?.username ?? '', password)
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
        <input id="username" name="username" type="text" autoComplete="username" value={session?.username ?? ''} readOnly tabIndex={-1} style={{ display: 'none' }} />
        <PasswordField id="new-password" name="new-password" label={t('auth.newPassword')} autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} helperText={t('auth.passwordHint')} required />
        <PasswordField id="confirm-password" name="confirm-password" label={t('auth.confirmPassword')} autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} error={Boolean(confirmation && confirmation !== password)} required />
        <Button variant="contained" size="large" type="submit" disabled={pending || !passwordValid}>{t('auth.completeSetup')}</Button>
      </Stack>
    </AuthLayout>
  )
}
