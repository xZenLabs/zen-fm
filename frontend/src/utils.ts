export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 1) return bytes === 0 ? '0 B' : '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** exponent).toLocaleString(undefined, { maximumFractionDigits: exponent ? 1 : 0 })} ${units[exponent]}`
}

export function formatDuration(seconds: number) {
  const roundedSeconds = Math.max(1, Math.ceil(seconds))
  if (roundedSeconds < 60) return `${roundedSeconds} ${roundedSeconds === 1 ? 'second' : 'seconds'}`
  const minutes = Math.ceil(roundedSeconds / 60)
  if (minutes < 60) return `${minutes} ${minutes === 1 ? 'minute' : 'minutes'}`
  const hours = Math.ceil(minutes / 60)
  return `${hours} ${hours === 1 ? 'hour' : 'hours'}`
}

export function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}

export function joinPath(parent: string, name: string) {
  return `${parent === '/' ? '' : parent}/${name}`.replaceAll('//', '/') || '/'
}

export function filesRoute(path: string) {
  if (path === '/') return '/files'
  return `/files${path.split('/').map((part) => encodeURIComponent(part)).join('/')}`
}

export function publicShareUrl(value: string) {
  const url = new URL(value, window.location.origin)
  if (url.pathname.startsWith('/share/')) {
    const secret = new URLSearchParams(url.hash.slice(1)).get('secret')
    if (secret) return new URL(`/s/${encodeURIComponent(secret)}`, url.origin).toString()
  }
  return url.toString()
}
