import { useEffect, useState } from 'react'
import QRCodeLib from 'qrcode'

/**
 * QRCode — renders a QR code client-side (no third-party service).
 * The connection string / subscription token never leaves the browser.
 * Dark modules on white for reliable scanning across apps.
 */
export default function QRCode({ data, size = 180, className = '' }) {
  const [src, setSrc] = useState('')

  useEffect(() => {
    if (!data) { setSrc(''); return }
    let cancelled = false
    QRCodeLib.toDataURL(data, {
      width: size,
      margin: 1,
      errorCorrectionLevel: 'M',
      color: { dark: '#0a130f', light: '#ffffff' },
    })
      .then(url => { if (!cancelled) setSrc(url) })
      .catch(() => { if (!cancelled) setSrc('') })
    return () => { cancelled = true }
  }, [data, size])

  if (!src) {
    return <div style={{ width: size, height: size }} className={`rounded-lg bg-white/5 border border-brand/10 animate-pulse ${className}`} aria-hidden="true" />
  }
  return (
    <img
      src={src}
      alt="QR-код для подключения"
      width={size}
      height={size}
      loading="lazy"
      className={`rounded-lg bg-white p-2 ${className}`}
    />
  )
}
