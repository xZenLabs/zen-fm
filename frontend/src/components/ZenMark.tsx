import { Box, Typography } from '@mui/material'

export function ZenMark({ compact = false, size = 34 }: { compact?: boolean; size?: number }) {
  return (
    <Box display="flex" alignItems="center" gap={1.2} color="text.primary">
      <Box component="img" className="zen-mark" src="/zen-fm.svg" alt="" aria-hidden="true" style={{ width: size, height: size, flexBasis: size }} />
      {!compact && <Typography fontSize="1.08rem" fontWeight={700} letterSpacing="-.025em">ZenFM</Typography>}
    </Box>
  )
}
