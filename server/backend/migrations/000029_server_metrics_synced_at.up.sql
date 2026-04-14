-- Отдельное поле для времени последней успешной синхронизации метрик.
-- Ранее synced_at хранился в load_metrics jsonb, что делало его ненадёжным
-- и требовало парсинга JSON при каждом чтении.

ALTER TABLE servers ADD COLUMN IF NOT EXISTS metrics_synced_at TIMESTAMPTZ;

-- Инициализация для уже существующих записей — копируем из jsonb, если там есть
UPDATE servers
SET metrics_synced_at = (load_metrics->>'synced_at')::timestamptz
WHERE load_metrics IS NOT NULL
  AND load_metrics->>'synced_at' IS NOT NULL
  AND metrics_synced_at IS NULL;
