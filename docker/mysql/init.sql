CREATE TABLE IF NOT EXISTS users (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    api_access_token VARCHAR(255) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY users_api_access_token_unique (api_access_token)
);

INSERT INTO users (api_access_token) VALUES ('dev-token');
