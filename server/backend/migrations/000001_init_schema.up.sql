CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL UNIQUE,
    telegram_id BIGINT UNIQUE,
    role VARCHAR(50) DEFAULT 'user',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_groups (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    is_premium BOOLEAN DEFAULT FALSE,
    company_id INT
);

CREATE TABLE IF NOT EXISTS user_subscriptions (
    user_id INT REFERENCES users(id) ON DELETE CASCADE,
    group_id INT REFERENCES user_groups(id) ON DELETE CASCADE,
    valid_until TIMESTAMP WITH TIME ZONE NOT NULL,
    auto_renew BOOLEAN DEFAULT FALSE,
    PRIMARY KEY (user_id, group_id)
);

CREATE TABLE IF NOT EXISTS servers (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    ip_address VARCHAR(50) NOT NULL,
    ssh_port INT DEFAULT 22,
    ssh_user VARCHAR(100) DEFAULT 'root',
    ssh_key_id VARCHAR(255),
    country_code VARCHAR(10),
    status VARCHAR(50) DEFAULT 'offline',
    load_metrics JSONB DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS server_groups (
    server_id INT REFERENCES servers(id) ON DELETE CASCADE,
    group_id INT REFERENCES user_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (server_id, group_id)
);

CREATE TABLE IF NOT EXISTS promocodes (
    code VARCHAR(50) PRIMARY KEY,
    group_id_granted INT REFERENCES user_groups(id),
    duration_days INT NOT NULL,
    max_uses INT DEFAULT 1,
    current_uses INT DEFAULT 0,
    expires_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS payments (
    id SERIAL PRIMARY KEY,
    user_id INT REFERENCES users(id),
    amount DECIMAL(10, 2) NOT NULL,
    status VARCHAR(50) NOT NULL,
    provider VARCHAR(50),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Insert default free group
INSERT INTO user_groups (name, description, is_premium) VALUES ('Free', 'Default free tier', false) ON CONFLICT DO NOTHING;
