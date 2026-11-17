UPDATE app_settings s SET value = b.value, updated_at = b.updated_at
  FROM app_settings_bak_20261113 b WHERE b.key = 'ghost_auto_cleanup' AND s.key = b.key;
