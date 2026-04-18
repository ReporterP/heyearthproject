-- Mask historical sensitive values in admin_audit_log that were written in plaintext
-- before isSensitiveSettingKey check was added. One-time data scrub.
UPDATE admin_audit_log
SET details = jsonb_set(details, '{value}', '"***REDACTED***"'::jsonb)
WHERE action = 'update_setting'
  AND details IS NOT NULL
  AND details ? 'value'
  AND (
    LOWER(COALESCE(target_id, '')) LIKE '%secret%'
    OR LOWER(COALESCE(target_id, '')) LIKE '%token%'
    OR LOWER(COALESCE(target_id, '')) LIKE '%password%'
    OR LOWER(COALESCE(target_id, '')) LIKE '%api_key%'
    OR LOWER(COALESCE(target_id, '')) LIKE '%private_key%'
    OR LOWER(COALESCE(target_id, '')) LIKE '%webhook_key%'
    OR LOWER(COALESCE(target_id, '')) LIKE '%client_secret%'
  );
