package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

	// Свежая попытка в created (<30 c) — другой запрос ещё регистрирует оплату в Сбере.
	var pendingSince time.Time
	err = tx.QueryRow(ctx, `
		SELECT created_at FROM payments
		WHERE order_id = $1 AND status = $2
		ORDER BY attempt DESC LIMIT 1`, p.orderID, StatusCreated).Scan(&pendingSince)
	switch {
	case err == nil:
		if s.now().Before(pendingSince.Add(payInProgressWindow)) {
			return prep{}, ErrInProgress
		}
	case errors.Is(err, pgx.ErrNoRows):
		// нет незавершённых попыток — продолжаем
	default:
		return prep{}, err
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

	rows, err := tx.Query(ctx, `SELECT dish_id, title, price_kop, qty FROM order_items WHERE order_id = $1 ORDER BY id`, p.orderID)
	if err != nil {
		return prep{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			dishID int64
			it     sber.BundleItem
		)
		if err := rows.Scan(&dishID, &it.Name, &it.PriceKop, &it.Qty); err != nil {
			return prep{}, err
		}
		it.ItemCode = strconv.FormatInt(dishID, 10)
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

// autoCancelBuffer — запас после истечения сессии оплаты, чтобы не отменить заказ, пока
// callback/сверка ещё в пути.
const autoCancelBuffer = 5 * time.Minute

// AutoCancel отменяет онлайн-заказы, не оплаченные вовремя. Срок считается от created_at
// последней попытки оплаты (без попыток — от created_at заказа) + 20 минут + запас. Перед
// отменой обязательно проверяет оплату в Сбере. now передаётся параметром для тестов.
func (s *Service) AutoCancel(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-(sessionTimeout + autoCancelBuffer))
	rows, err := s.pool.Query(ctx, `
		SELECT o.id FROM orders o
		LEFT JOIN LATERAL (
			SELECT created_at FROM payments p WHERE p.order_id = o.id ORDER BY attempt DESC LIMIT 1
		) lp ON true
		WHERE o.payment_method = $1 AND o.status IN ($2, $3)
		  AND COALESCE(lp.created_at, o.created_at) < $4`,
		orders.PaymentOnline, orders.StatusCreated, orders.StatusAwaitingPayment, cutoff)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}

	for _, orderID := range ids {
		pid, hasPayment, err := s.latestPaymentID(ctx, orderID)
		if err != nil {
			return err
		}
		var checked *int64
		if hasPayment {
			// Обязательная проверка в Сбере. Если не удалось проверить — не отменяем.
			if err := s.SyncPayment(ctx, pid); err != nil {
				s.log.Error("автоотмена: не смогли проверить оплату в Сбере", "order_id", orderID, "err", err)
				continue
			}
			st, err := s.paymentStatus(ctx, pid)
			if err != nil {
				return err
			}
			if st == StatusPaid {
				continue // оплата всё же прошла — не отменяем
			}
			checked = &pid
		}
		if err := s.cancelIfUnpaid(ctx, orderID, checked); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) paymentStatus(ctx context.Context, paymentID int64) (string, error) {
	var st string
	err := s.pool.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1`, paymentID).Scan(&st)
	return st, err
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

// cancelIfUnpaid отменяет заказ, если он ещё ждёт оплату. checkedPayment — платёж, по
// которому Сбер подтвердил, что он не оплачен: только его помечаем expired.
func (s *Service) cancelIfUnpaid(ctx context.Context, orderID int64, checkedPayment *int64) error {
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
	// Помечаем expired только проверенный платёж и только если он ещё ждал оплату.
	if checkedPayment != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET status = $1, updated_at = now() WHERE id = $2 AND status = $3`,
			StatusExpired, *checkedPayment, StatusRegistered); err != nil {
			return err
		}
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
