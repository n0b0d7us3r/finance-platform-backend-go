#!/bin/sh
# Erase IP addresses stored in cabinet sign-in history.
# Login rows, users, dates and devices stay.
set -eu

container="${MYSQL_CONTAINER:-finance-go-mysql-1}"
database="${DB_DATABASE:-dev-app}"

docker exec "$container" mysql -uroot "$database" -e "
UPDATE user_logins
SET ip = ''
WHERE ip <> '' AND ip <> '—';
SELECT ROW_COUNT() AS cleared;
"
