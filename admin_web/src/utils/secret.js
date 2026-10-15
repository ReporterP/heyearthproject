// The API's own answer to an empty secret (UpdateSettingHandler, internal/admin/settings_handlers.go).
// A value of only whitespace is the same empty secret, so it is refused here with the same words.
export const EMPTY_SECRET_ERROR =
  'Пустое значение не сохраняется: секрет хранится только на сервере, поле в админке всегда пустое. Введите новое значение, чтобы заменить.'

// prepareSecret: what a write-only secret field sends. Surrounding whitespace (a pasted key with a
// trailing space or newline) is dropped: stored as is, it would silently break the key. Nothing
// left means nothing to save.
export function prepareSecret(raw) {
  const value = String(raw ?? '').trim()
  return value ? { value, error: '' } : { value: '', error: EMPTY_SECRET_ERROR }
}
