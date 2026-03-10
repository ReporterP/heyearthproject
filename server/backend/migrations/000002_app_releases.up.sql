CREATE TABLE IF NOT EXISTS app_releases (
    id SERIAL PRIMARY KEY,
    platform VARCHAR(20) NOT NULL,
    version VARCHAR(50) NOT NULL,
    release_notes TEXT,
    file_name VARCHAR(255) NOT NULL,
    file_path TEXT NOT NULL,
    public_path TEXT NOT NULL,
    content_type VARCHAR(255),
    file_size BIGINT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT app_releases_platform_check CHECK (platform IN ('android', 'windows', 'macos', 'linux', 'ios')),
    CONSTRAINT app_releases_platform_version_unique UNIQUE (platform, version)
);

CREATE INDEX IF NOT EXISTS idx_app_releases_platform_active
    ON app_releases (platform, is_active, updated_at DESC);
