-- Columns this module queries. Not the migration source of truth:
-- schema changes stay in Laravel until the goose baseline.
CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  api_access_token VARCHAR(255) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY users_api_access_token_unique (api_access_token)
);
