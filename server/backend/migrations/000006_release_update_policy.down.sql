ALTER TABLE app_releases
    DROP COLUMN IF EXISTS min_supported_version,
    DROP COLUMN IF EXISTS mandatory_update;
