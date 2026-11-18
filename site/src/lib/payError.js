// payErrorText: what the payment and top-up buttons say when opening a payment failed. The
// server's own reason wins (400 — the request; 502/503 — payments are off or YooKassa didn't
// answer), else the catalogue line (spec 05 §7).
export function payErrorText(e) {
  if (e?.message && (e.status === 400 || e.kind === 'unavailable')) return e.message
  return 'Не удалось открыть оплату — попробуйте ещё раз.'
}
