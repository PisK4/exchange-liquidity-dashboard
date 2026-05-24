CREATE TABLE IF NOT EXISTS t_exchange_instrument_catalog (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    platform VARCHAR(32) NOT NULL,
    api_symbol VARCHAR(96) NOT NULL,
    status VARCHAR(32) NOT NULL,
    updated_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS t_platform_volume_snapshot (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    platform VARCHAR(32) NOT NULL,
    snapshot_ts TIMESTAMP NOT NULL,
    volume_24h_usd DECIMAL(28,8),
    discount DECIMAL(10,4)
);

CREATE TABLE IF NOT EXISTS t_runtime_config (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    config_key VARCHAR(96) NOT NULL UNIQUE,
    config_value TEXT NOT NULL,
    updated_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
