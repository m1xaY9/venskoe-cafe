-- +goose Up
-- needs_refund = true, если оплата пришла уже после отмены заказа: деньги нужно вернуть
-- гостю (возврат реализуем в ветке feature/refunds).
ALTER TABLE payments ADD COLUMN needs_refund boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE payments DROP COLUMN needs_refund;
