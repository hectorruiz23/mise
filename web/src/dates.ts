const dateTimeFormat = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  timeZone: 'Europe/Madrid',
})

const dateFormat = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
  timeZone: 'Europe/Madrid',
})

const shortDateFormat = new Intl.DateTimeFormat('en-GB', {
  day: '2-digit',
  month: 'short',
  timeZone: 'Europe/Madrid',
})

export function formatDateTime(seconds: number): string {
  return dateTimeFormat.format(new Date(seconds * 1000))
}

export function formatDate(seconds: number): string {
  return dateFormat.format(new Date(seconds * 1000))
}

export function formatShortDate(seconds: number): string {
  return shortDateFormat.format(new Date(seconds * 1000))
}

export function isoDate(seconds: number): string {
  return new Date(seconds * 1000).toISOString()
}

export function leaseMinutesLeft(expiresAt: number, now: number): number {
  return Math.max(0, Math.ceil((expiresAt - now) / 60))
}

export function formatRelative(seconds: number, now: number): string {
  const elapsed = Math.max(0, now - seconds)
  if (elapsed < 60) return 'just now'
  const minutes = Math.floor(elapsed / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} d ago`
  const months = Math.floor(days / 30)
  if (months < 12) return `${months} mo ago`
  const years = Math.floor(days / 365)
  return `${years} y ago`
}
