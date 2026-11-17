// copyText — the clipboard API where the page is a secure context, a hidden textarea otherwise.
// Resolves to true when the text was copied.
export async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch { /* fall back below */ }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.left = '-9999px'
  ta.style.top = '0'
  document.body.appendChild(ta)
  ta.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(ta)
  }
}

// selectContents selects the text of an element so the person can copy it by hand (03 §8).
export function selectContents(el) {
  if (!el) return
  const r = document.createRange()
  r.selectNodeContents(el)
  const s = window.getSelection()
  s.removeAllRanges()
  s.addRange(r)
}

export const isPhone = () => typeof window !== 'undefined' && window.matchMedia('(max-width: 640px)').matches
