-- Read paths for V1. Statements are executed from internal/store.

-- name: FindOrderByUUID :one
SELECT
    o.id,
    o.uuid,
    o.external_id,
    o.base_amount,
    o.amount,
    o.total_profit,
    o.merchant_profit,
    o.service_profit,
    o.currency,
    o.base_conversion_price,
    o.conversion_price,
    o.trader_commission_rate,
    o.total_service_commission_rate,
    o.status,
    o.sub_status,
    o.callback_url,
    o.finished_at,
    o.expires_at,
    o.created_at,
    m.uuid,
    m.name,
    m.description,
    m.user_id,
    pg.code,
    pg.nspk_schema,
    pg.name,
    pg.is_transgran,
    pg.is_intrabank,
    pd.detail,
    pd.detail_type,
    pd.initials,
    pd.user_id,
    d.status,
    d.cancel_reason,
    d.reason
FROM orders o
INNER JOIN merchants m ON m.id = o.merchant_id
LEFT JOIN payment_gateways pg ON pg.id = o.payment_gateway_id
LEFT JOIN payment_details pd ON pd.id = o.payment_detail_id
LEFT JOIN disputes d ON d.id = (
    SELECT id FROM disputes WHERE order_id = o.id ORDER BY id DESC LIMIT 1
)
WHERE o.uuid = ?
LIMIT 1;

-- name: FindOrderByExternal :one
-- Same columns. WHERE m.uuid = ? AND o.external_id = ?

-- name: UserHasRole :one
SELECT 1
FROM user_roles ur
INNER JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = ?
  AND r.name = ?
LIMIT 1;

-- name: MerchantBalance :one
SELECT merchant_balance
FROM wallets
WHERE user_id = ?
LIMIT 1;

-- name: ListActivePaymentGateways :many
SELECT
    name,
    code,
    nspk_schema,
    currency,
    min_limit,
    max_limit,
    reservation_time_for_orders,
    detail_types,
    is_transgran,
    is_intrabank,
    total_service_commission_rate_for_orders,
    total_service_commission_rate_for_payouts,
    trader_commission_rate_for_orders,
    trader_commission_rate_for_payouts
FROM payment_gateways
WHERE is_active = 1
ORDER BY id;
