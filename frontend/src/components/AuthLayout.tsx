import { Box, Paper, Stack, Typography } from '@mui/material'
import type { PropsWithChildren, ReactNode } from 'react'
import { ZenMark } from './ZenMark'

export function AuthLayout({ title, subtitle, inlineLogo = false, children }: PropsWithChildren<{ title: string; subtitle: ReactNode; inlineLogo?: boolean }>) {
  return (
    <Box minHeight="100dvh" display="grid" sx={{ placeItems: 'center', px: 2, py: 5, background: 'radial-gradient(circle at 50% 0%, color-mix(in srgb, var(--mui-palette-primary-main) 11%, transparent), transparent 40%)' }}>
      <Paper component="main" sx={{ width: '100%', maxWidth: 430, p: { xs: 3, sm: 4.5 }, border: 1, borderColor: 'divider' }}>
        <Stack gap={3}>
          {!inlineLogo && <ZenMark />}
          <Box>
            <Box display="flex" alignItems="center" gap={1.5} mb={1}>
              {inlineLogo && <ZenMark compact size={52} />}
              <Typography variant="h1">{title}</Typography>
            </Box>
            <Typography color="text.secondary">{subtitle}</Typography>
          </Box>
          {children}
        </Stack>
      </Paper>
    </Box>
  )
}
