import { useState, type FormEvent } from 'react'
import { Alert, Button, Stack, TextField } from '@mui/material'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../auth/AuthProvider'
import { AuthLayout } from '../components/AuthLayout'

export function LoginPage() {
  const { t } = useTranslation()
  const { login } = useAuth()
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setPending(true)
    setError('')
    try {
      const session = await login(username.trim(), password)
      void navigate(session.setupRequired ? '/setup' : '/files', { replace: true })
    } catch {
      setError(t('auth.failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthLayout title={t('appName')} subtitle={t('auth.welcome')}>
      <Stack component="form" gap={2} onSubmit={(event) => void submit(event)}>
        {error && <Alert severity="error">{error}</Alert>}
        <TextField label={t('auth.username')} value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" autoFocus required />
        <TextField label={t('auth.password')} type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required />
        <Button variant="contained" size="large" type="submit" disabled={pending || !username || !password}>{pending ? t('auth.signingIn') : t('auth.signIn')}</Button>
      </Stack>
    </AuthLayout>
  )
}
