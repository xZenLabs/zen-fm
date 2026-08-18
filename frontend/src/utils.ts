export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 1) return bytes === 0 ? '0 B' : '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** exponent).toLocaleString(undefined, { maximumFractionDigits: exponent ? 1 : 0 })} ${units[exponent]}`
}

export function formatDuration(seconds: number) {
  const roundedSeconds = Math.max(1, Math.ceil(seconds))
  if (roundedSeconds < 60) return `${roundedSeconds} ${roundedSeconds === 1 ? 'second' : 'seconds'}`
  const minutes = Math.floor(roundedSeconds / 60)
  const remainingSeconds = roundedSeconds % 60
  if (minutes < 60) return `${minutes} ${minutes === 1 ? 'minute' : 'minutes'}${remainingSeconds ? ` ${remainingSeconds} ${remainingSeconds === 1 ? 'second' : 'seconds'}` : ''}`
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return `${hours} ${hours === 1 ? 'hour' : 'hours'}${remainingMinutes ? ` ${remainingMinutes} ${remainingMinutes === 1 ? 'minute' : 'minutes'}` : ''}`
}

export class TransferEtaEstimator {
  private readonly samples: Array<{ at: number; bytes: number }> = []
  private completionAt = 0

  update(transferredBytes: number, remainingBytes: number, now: number) {
    const last = this.samples[this.samples.length - 1]
    if (!last || transferredBytes > last.bytes) this.samples.push({ at: now, bytes: transferredBytes })
    while (this.samples.length > 2 && this.samples[1]!.at < now - 8_000) this.samples.shift()

    const first = this.samples[0]
    const current = this.samples[this.samples.length - 1]
    if (!first || !current || current.bytes <= first.bytes || current.at - first.at < 500 || remainingBytes <= 0) return this.completionAt

    const bytesPerSecond = (current.bytes - first.bytes) / ((current.at - first.at) / 1_000)
    const candidate = now + remainingBytes / bytesPerSecond * 1_000
    if (this.completionAt === 0) this.completionAt = candidate
    else {
      const weight = candidate > this.completionAt ? 0.12 : 0.35
      this.completionAt += (candidate - this.completionAt) * weight
    }
    return this.completionAt
  }
}

export function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}

export function formatShortDate(value?: string, locale?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat(locale, { day: '2-digit', month: '2-digit', year: '2-digit' }).format(date)
}

export function joinPath(parent: string, name: string) {
  return `${parent === '/' ? '' : parent}/${name}`.replaceAll('//', '/') || '/'
}

export function filesRoute(path: string) {
  if (path === '/') return '/files'
  return `/files${path.split('/').map((part) => encodeURIComponent(part)).join('/')}`
}

export function fileRoute(path: string) {
  const separator = path.lastIndexOf('/')
  const parent = path.slice(0, separator) || '/'
  const query = new URLSearchParams({ file: path.slice(separator + 1) })
  return `${filesRoute(parent)}?${query}`
}

export function publicShareUrl(value: string) {
  const url = new URL(value, window.location.origin)
  if (url.pathname.startsWith('/share/')) {
    const secret = new URLSearchParams(url.hash.slice(1)).get('secret')
    if (secret) return new URL(`/s/${encodeURIComponent(secret)}`, url.origin).toString()
  }
  return url.toString()
}
