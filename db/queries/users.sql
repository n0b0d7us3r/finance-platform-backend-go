-- name: FindUserIDByAccessToken :one
SELECT id
FROM users
WHERE api_access_token = ?
LIMIT 1;
