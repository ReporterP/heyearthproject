import { useState, useCallback } from 'react'

/* Copy helper with fallback for HTTP */
async function copyToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  // Fallback for HTTP
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '-9999px'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  try {
    document.execCommand('copy')
    return true
  } catch {
    return false
  } finally {
    document.body.removeChild(textarea)
  }
}

const CopyIcon = () => (
  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75" />
  </svg>
)
const CheckIcon = () => (
  <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
  </svg>
)

/**
 * Reusable copy-to-clipboard button.
 *
 * Props:
 *   text     - string to copy
 *   label    - optional override for "Kopirovat'" label
 *   compact  - if true, renders icon-only button (no text)
 */
export default function CopyButton({ text, label, compact = false }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(async () => {
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }, [text])

  if (compact) {
    return (
      <button
        onClick={handleCopy}
        className="p-1 rounded hover:bg-white/10 transition-colors flex-shrink-0"
        title={copied ? 'Скопировано!' : 'Копировать'}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </button>
    )
  }

  return (
    <button
      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium glass hover:bg-white/10 transition-all duration-200"
      onClick={handleCopy}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
      {copied ? 'Скопировано!' : (label || 'Копировать')}
    </button>
  )
}

export { copyToClipboard }
