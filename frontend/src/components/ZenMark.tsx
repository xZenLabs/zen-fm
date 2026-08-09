import { Box, Typography } from '@mui/material'

export function ZenMark({ compact = false }: { compact?: boolean }) {
  return (
    <Box display="flex" alignItems="center" gap={1.2} color="text.primary">
      <Box component="img" className="zen-mark" src="/zen-fm.svg" alt="" aria-hidden="true" />
      {!compact && <Typography fontSize="1.08rem" fontWeight={700} letterSpacing="-.025em">ZenFM</Typography>}
    </Box>
  )
}
