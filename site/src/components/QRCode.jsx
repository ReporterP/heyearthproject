import { useEffect, useState } from 'react'
import Dialog, { DialogActions } from './Dialog.jsx'
import Button from './Button.jsx'

// QR (spec 02 §19): generated in the browser (the subscription link never leaves it), shown only in
// a dialog. White plate — the scanner needs it (01 §10 allows #fff here).
export function QR({ data, size = 184, label = 'QR-код ссылки подписки' }) {
  const [src, setSrc] = useState('')
  useEffect(() => {
    if (!data) { setSrc(''); return undefined }
    let alive = true
    import('qrcode')
      .then(({ default: Q }) => Q.toDataURL(data, { width: size * 2, margin: 0, errorCorrectionLevel: 'M' }))
      .then((u) => { if (alive) setSrc(u) })
      .catch(() => { if (alive) setSrc('') })
    return () => { alive = false }
  }, [data, size])
  return (
    <div className="qr" role="img" aria-label={label}>
      {src ? <img src={src} alt="" width={size} height={size} style={{ display: 'block' }} /> : <span className="spinner" aria-hidden="true" style={{ margin: (size - 28) / 2 }} />}
    </div>
  )
}

// The QR dialog of 08 §8.3, opened from the overview, /connect and /connect?sub=.
export default function QRDialog({ open, onClose, data, title = 'Отсканируйте с телефона', text }) {
  return (
    <Dialog open={open} onClose={onClose} title={title}>
      <div style={{ display: 'grid', placeItems: 'center', margin: '20px 0 16px' }}>
        <QR data={data} size={typeof window !== 'undefined' && window.innerWidth <= 640 ? 200 : 184} />
      </div>
      <p className="muted t-sm">
        {text || <>Камерой телефона или из Happ: «+» → «Сканировать QR». Не показывайте код посторонним — это доступ к вашей подписке.</>}
      </p>
      <DialogActions>
        <Button variant="secondary" block onClick={onClose} data-autofocus>Закрыть</Button>
      </DialogActions>
    </Dialog>
  )
}
