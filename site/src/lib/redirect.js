// A ?redirect= target is only ever a path on this site ("/payment", "/claim/x"), never
// another origin ("//evil", "https://…", "/\evil"); anything else falls back.
export function safeRedirect(raw, fallback = '/dashboard') {
  if (typeof raw !== 'string' || !raw.startsWith('/') || raw.startsWith('//') || raw.includes('\\')) return fallback
  return raw
}
