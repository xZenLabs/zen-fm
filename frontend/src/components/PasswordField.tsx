import { useState } from 'react'
import { IconButton, InputAdornment, TextField, Tooltip, type TextFieldProps } from '@mui/material'
import VisibilityRounded from '@mui/icons-material/VisibilityRounded'
import VisibilityOffRounded from '@mui/icons-material/VisibilityOffRounded'
import { useTranslation } from 'react-i18next'

export function PasswordField(props: Omit<TextFieldProps, 'type' | 'InputProps'>) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)

  return <TextField {...props} type={visible ? 'text' : 'password'} InputProps={{
    endAdornment: <InputAdornment position="end">
      <Tooltip title={visible ? t('auth.hidePassword') : t('auth.showPassword')}>
        <IconButton
          type="button"
          edge="end"
          aria-label={visible ? t('auth.hidePassword') : t('auth.showPassword')}
          aria-pressed={visible}
          onClick={() => setVisible((current) => !current)}
          onMouseDown={(event) => event.preventDefault()}
        >
          {visible ? <VisibilityOffRounded /> : <VisibilityRounded />}
        </IconButton>
      </Tooltip>
    </InputAdornment>,
  }} />
}
