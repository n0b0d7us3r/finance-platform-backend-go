-- +goose Up
UPDATE `roles` SET `name` = 'Admin' WHERE `name` = 'Super Admin';

-- +goose Down
UPDATE `roles` SET `name` = 'Super Admin' WHERE `name` = 'Admin';
