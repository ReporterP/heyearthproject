// Protocol names as the owner and the clients see them (C12): never VLESS/HYSTERIA2/XHTTP in the UI.
export const PROTOCOL_NAMES = {
  all: 'Все',
  vless: 'Берёза',
  'vless-reality': 'Берёза',
  hysteria2: 'Бамбук',
  xhttp: 'Дуб',
}
export const protocolName = (key) => PROTOCOL_NAMES[String(key || '').toLowerCase()] || key

// Payment statuses in words.
export const PAYMENT_STATUS_NAMES = {
  pending: 'ожидает', succeeded: 'оплачен', completed: 'оплачен', failed: 'ошибка', canceled: 'отменён',
  waiting_for_capture: 'ждёт подтверждения', refunded: 'возвращён', partially_refunded: 'возвращён частично',
}
export const paymentStatusName = (s) => PAYMENT_STATUS_NAMES[s] || s
