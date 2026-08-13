import { formatDuration, formatShortDate, TransferEtaEstimator } from '../utils'

describe('transfer estimates', () => {
  it('shows seconds within multi-minute estimates', () => {
    expect(formatDuration(61)).toBe('1 minute 1 second')
    expect(formatDuration(133)).toBe('2 minutes 13 seconds')
    expect(formatDuration(3_661)).toBe('1 hour 1 minute')
    expect(formatDuration(3_725)).toBe('1 hour 2 minutes')
  })

  it('dampens slower throughput samples instead of replacing the ETA', () => {
    const estimator = new TransferEtaEstimator()
    expect(estimator.update(0, 1_000, 0)).toBe(0)
    const first = estimator.update(100, 900, 1_000)
    const slower = estimator.update(150, 850, 2_000)

    expect(first).toBe(10_000)
    expect(slower).toBeGreaterThan(first)
    expect(slower).toBeLessThan(10_500)
  })
})

describe('short dates', () => {
  it('uses locale order with a two-digit year', () => {
    const date = '2026-01-02T12:00:00Z'

    expect(formatShortDate(date, 'en-US')).toBe('01/02/26')
    expect(formatShortDate(date, 'en-GB')).toBe('02/01/26')
  })
})
