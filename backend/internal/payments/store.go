package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"venskoe/backend/internal/orders"
	"venskoe/backend/internal/sber"

	"github.com/jackc/pgx/v5"
)

// prep — всё, что нужно для регистрации в Сбере, подготовленное в одной транзакции.
type prep struct {
	orderID     int64
	orderNumber string
	paymentID   int64
	totalKop    int64
	deliveryKop int64
	email       string
	phone       string
	items       []sber.BundleItem
	reuseForm   string // непусто → переиспользуем живую попытку, регистрацию не делаем
}

// preparePayment под блокировкой заказа проверяет, что его можно оплатить, переиспользует
// живую попытку или заводит новую попытку оплаты.
func (s *Service) preparePayment(ctx context.Context, token string) (prep, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return prep{}, err
	}
	defer tx.Rollback(ctx)

	var (
		p             prep
		number        int64
		status        string
		paymentMethod string
	)
	err = tx.QueryRow(ctx, `
		SELECT id, number, status, payment_method, email, phone, total_kop, delivery_kop
		FROM orders WHERE public_token = $1 FOR UPDATE`, token).
		Scan(&p.orderID, &number, &status, &paymentMethod, &p.email, &p.phone, &p.totalKop, &p.deliveryKop)
	if errors.Is(err, pgx.ErrNoRows) {
		return prep{}, orders.ErrOrderNotFound
	}
	if err != nil {
		return prep{}, err
	}

	if paymentMethod != orders.PaymentOnline {
		return prep{}, ErrNotOnline
	}
	if status != orders.StatusCreated && status != orders.StatusAwaitingPayment {
		return prep{}, ErrNotPayable
	}

	// Живая попытка в registered, время не вышло — переиспользуем её форму.
	var (
		formURL   string
		createdAt time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT form_url, created_at FROM payments
		WHERE order_id = $1 AND status = $2
		ORDER BY attempt DESC LIMIT 1`, p.orderID, StatusRegistered).
		Scan(&formURL, &createdAt)
	switch {
	case err == nil:
		if formURL != "" && s.now().Before(createdAt.Add(sessionTimeout)) {
			p.reuseForm = formURL
			if err := tx.Commit(ctx); err != nil {
				return prep{}, err
			}
			return p, nil
		}
	case errors.Is(err, pgx.ErrNoRows):
		// живой попытки нет — заводим новую
	default:
		return prep{}, err
	}

	var maxAttempt int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(attempt), 0) FROM payments WHERE order_id = $1`, p.orderID).Scan(&maxAttempt); err != nil {
		return prep{}, err
	}
	attempt := maxAttempt + 1
	p.orderNumber = fmt.Sprintf("%d-%d", number, attempt)

	if err := tx.QueryRow(ctx, `
		INSERT INTO payments (order_id, attempt, sber_order_number, amount_kop, status)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		p.orderID, attempt, p.orderNumber, p.totalKop, StatusCreated).Scan(&p.paymentID); err != nil {
		return prep{}, fmt.Errorf("payments: вставка платежа: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT title, price_kop, qty FROM order_items WHERE order_id = $1 ORDER BY id`, p.orderID)
	if err != nil {
		return prep{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var it sber.BundleItem
		if err := rows.Scan(&it.Name, &it.PriceKop, &it.Qty); err != nil {
			return prep{}, err
		}
		p.items = append(p.items, it)
	}
	if err := rows.Err(); err != nil {
		return prep{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return prep{}, err
	}
	return p, nil
}

// markRegistered сохраняет orderId/formUrl и переводит заказ в awaiting_payment.
func (s *Service) markRegistered(ctx context.Context, paymentID, orderID int64, sberOrderID, formURL string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE payments SET sber_order_id = $1, form_url = $2, status = $3, updated_at = now() WHERE id = $4`,
		sberOrderID, formURL, StatusRegistered, paymentID); err != nil {
		return fmt.Errorf("payments: отметка registered: %w", err)
	}

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status); err != nil {
		return err
	}
	if status == orders.StatusCreated {
		if err := orders.Transition(ctx, tx, orderID, orders.StatusAwaitingPayment, orders.ActorSystem, "ожидается оплата"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// FindPaymentByCallback ищет платёж по mdOrder (orderId) или orderNumber из callback.
func (s *Service) FindPaymentByCallback(ctx context.Context, mdOrder, orderNumber string) (int64, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM payments
		WHERE ($1 <> '' AND sber_order_id = $1) OR ($2 <> '' AND sber_order_number = $2)
		ORDER BY id DESC LIMIT 1`, mdOrder, orderNumber).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// SyncByOrderToken синхронизирует последнюю попытку оплаты заказа (для возврата гостя).
func (s *Service) SyncByOrderToken(ctx context.Context, token string) error {
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT p.id FROM payments p
		JOIN orders o ON o.id = p.order_id
		WHERE o.public_token = $1
		ORDER BY p.attempt DESC LIMIT 1`, token).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // платежей по заказу ещё нет — синхронизировать нечего
	}
	if err != nil {
		return err
	}
	return s.SyncPayment(ctx, id)
}

// ReconcilePending сверяет статус всех платежей в registered. SKIP LOCKED внутри syncInTx
// не даёт двум процессам обрабатывать один платёж.
func (s *Service) ReconcilePending(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT id FROM payments WHERE status = $1`, StatusRegistered)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.syncInTx(ctx, id, true); err != nil {
			return err
		}
	}
	return nil
}

// AutoCancel отменяет онлайн-заказы, не оплаченные за 20 минут. Перед отменой делает
// финальную проверку в Сбере. now передаётся параметром — удобно тестировать.
func (s *Service) AutoCancel(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-sessionTimeout)
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM orders
		WHERE payment_method = $1 AND status IN ($2, $3) AND created_at < $4`,
		orders.PaymentOnline, orders.StatusCreated, orders.StatusAwaitingPayment, cutoff)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}

	for _, orderID := range ids {
		// Финальная проверка в Сбере: вдруг оплата всё же прошла.
		if pid, ok, err := s.latestPaymentID(ctx, orderID); err != nil {
			return err
		} else if ok {
			if err := s.SyncPayment(ctx, pid); err != nil {
				return err
			}
		}
		if err := s.cancelIfUnpaid(ctx, orderID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) latestPaymentID(ctx context.Context, orderID int64) (int64, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM payments WHERE order_id = $1 ORDER BY attempt DESC LIMIT 1`, orderID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (s *Service) cancelIfUnpaid(ctx context.Context, orderID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status); err != nil {
		return err
	}
	if status != orders.StatusCreated && status != orders.StatusAwaitingPayment {
		return tx.Commit(ctx) // уже оплачен или отменён — не трогаем
	}
	if err := orders.Transition(ctx, tx, orderID, orders.StatusCancelled, orders.ActorSystem, "не оплачен за 20 минут"); err != nil {
		return err
	}
	// Незавершённые попытки помечаем просроченными.
	if _, err := tx.Exec(ctx,
		`UPDATE payments SET status = $1, updated_at = now() WHERE order_id = $2 AND status IN ($3, $4)`,
		StatusExpired, orderID, StatusCreated, StatusRegistered); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// rawJSON сериализует ответ статуса для хранения (секретов в нём нет).
func rawJSON(resp sber.StatusResponse) []byte {
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return b
}
