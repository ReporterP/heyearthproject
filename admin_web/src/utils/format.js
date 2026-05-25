/**
 * Shared formatting utilities for the admin panel.
 */

/**
 * Format byte count into a human-readable string (B / KB / MB / GB / TB).
 * Returns '0 B' for zero/falsy values.
 */
export function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B'
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return (bytes / Math.pow(1024, i)).toFixed(i > 1 ? 1 : 0) + ' ' + sizes[i]
}

/**
 * Format an ISO date string into a localized date+time string (ru-RU).
 * Example: "09.04.2026, 14:30"
 */
export function formatDate(iso) {
  if (!iso) return '\u2014'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleDateString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/**
 * Format an ISO date string into a short date-only string (ru-RU).
 * Example: "09.04.2026"
 */
export function formatDateShort(iso) {
  if (!iso) return '\u2014'
  return new Date(iso).toLocaleDateString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  })
}

/**
 * Format an ISO date string into a relative time label.
 * Examples: "5s ago", "12m ago", "3h ago", "2d ago"
 */
export function formatRelativeTime(isoString) {
  if (!isoString) return ''
  try {
    const date = new Date(isoString)
    const now = new Date()
    const diffMs = now - date
    const diffSec = Math.floor(diffMs / 1000)
    if (diffSec < 60) return `${diffSec}s ago`
    const diffMin = Math.floor(diffSec / 60)
    if (diffMin < 60) return `${diffMin}m ago`
    const diffH = Math.floor(diffMin / 60)
    if (diffH < 24) return `${diffH}h ago`
    const diffD = Math.floor(diffH / 24)
    return `${diffD}d ago`
  } catch {
    return isoString
  }
}

/**
 * Russian-localized relative time label suitable for table cells.
 * < 60 sec        → "1 мин"  (rounds up to 1)
 * < 60 min        → "12 мин"
 * < 24 h          → "3 ч"
 * <= 30 d         → "6 дн"
 * > 30 d or empty → falls back to formatDateShort / "—"
 */
export function formatRelativeRu(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const diffSec = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000))
  if (diffSec < 60)         return '1 мин'
  if (diffSec < 3600)       return `${Math.floor(diffSec / 60)} мин`
  if (diffSec < 86400)      return `${Math.floor(diffSec / 3600)} ч`
  if (diffSec < 86400 * 30) return `${Math.floor(diffSec / 86400)} дн`
  return formatDateShort(iso)
}
