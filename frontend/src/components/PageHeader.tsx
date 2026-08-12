import type { ReactNode } from 'react'
import { Box, Stack, Typography } from '@mui/material'

interface PageHeaderProps {
  title: ReactNode
  children?: ReactNode
  middle?: ReactNode
  actions?: ReactNode
}

export function PageHeader({ title, children, middle, actions }: PageHeaderProps) {
  return (
    <Stack className="page-header" direction={{ xs: 'column', md: 'row' }} justifyContent="space-between" gap={2} alignItems={{ md: 'center' }}>
      <Box minWidth={0} flexShrink={0}>
        <Typography variant="h1">{title}</Typography>
        <Box mt={0.75} minHeight={24}>{children}</Box>
      </Box>
      {middle && <Box flex={1} minWidth={0}>{middle}</Box>}
      {actions && <Box flexShrink={0}>{actions}</Box>}
    </Stack>
  )
}
