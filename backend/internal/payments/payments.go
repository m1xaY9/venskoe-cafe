package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"venskoe/backend/internal/orders"
	"venskoe/backend/internal/sber"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Статусы платежа.
const (
	StatusCreated    = "created"    // запись заведена, ещё не зарегистрирована в Сбере
	StatusRegistered = "registered" // зарегистрирована, ждём оплату
	StatusPaid       = "paid"
	StatusDeclined   = "declined"
	StatusExpired    = "expired"
	StatusReversed   = "reversed"
	StatusRefunded   = "refunded"
)

// sessionTimeout — сколько живёт попытка оплаты (sessionTimeoutSecs = 1200 с).
const sessionTimeout = 20 * time.Minute

var (
	ErrDisabled   = errors.New("payments: онлайн-оплата не настроена")
	ErrNotOnline  = errors.New("payments: заказ не с онлайн-оплатой")
	ErrNotPayable = errors.New("payments: заказ нельзя оплатить в текущем статусе")
	ErrInProgress = errors.New("payments: оплата уже создаётся")
)

// payInProgressWindow — если попытка в created моложе этого срока, считаем, что оплата уже
// создаётся (другой запрос ещё регистрирует её в Сбере), и не плодим новую.
const payInProgressWindow = 30 * time.Second

// SberClient — то, что нужно платежам от клиента Сбера. В тестах подменяется заглушкой.
type SberClient interface {
	Register(ctx context.Context, req sber.RegisterRequest) (sber.RegisterResponse, error)
	GetOrderStatusExtended(ctx context.Context, req sber.StatusRequest) (sber.StatusResponse, error)
	BuildOrderBundle(items []sber.BundleItem, deliveryKop int64) *sber.OrderBundle
}

// Service — оплата заказов через Сбер.
type Service struct {
	pool          *pgxpool.Pool
	sber          SberClient
	log           *slog.Logger
	returnURL     string
	failURL       string
	publicBaseURL string
	enabled       bool
	now           func() time.Time
}

func NewService(pool *pgxpool.Pool, client SberClient, log *slog.Logger, returnURL, failURL, publicBaseURL string, enabled bool) *Service {
	return &Service{
		pool:          pool,
		sber:          client,
		log:           log,
		returnURL:     returnURL,
		failURL:       failURL,
		publicBaseURL: publicBaseURL,
		enabled:       enabled,
		now:           time.Now,
	}
}

// Enabled — настроена ли онлайн-оплата.
func (s *Service) Enabled() bool { return s.enabled }

// PayResult — ответ на запрос оплаты: ссылка на платёжную форму Сбера.
type PayResult struct {
	FormURL string `json:"form_url"`
}

// Pay создаёт (или переиспользует) попытку оплаты онлайн-заказа и возвращает ссылку на
// форму оплаты. Если есть живая попытка в registered и время не вышло — возвращает её.
func (s *Service) Pay(ctx context.Context, token string) (PayResult, error) {
	if !s.enabled {
		return PayResult{}, ErrDisabled
	}

	prep, err := s.preparePayment(ctx, token)
	if err != nil {
		return PayResult{}, err
	}
	if prep.reuseForm != "" {
		return PayResult{FormURL: prep.reuseForm}, nil
	}

	// Регистрируем заказ в Сбере (сетевой вызов — вне транзакции).
	bundle := s.sber.BuildOrderBundle(prep.items, prep.deliveryKop)
	regReq := sber.RegisterRequest{
		OrderNumber:        prep.orderNumber,
		Amount:             prep.totalKop,
		ReturnURL:          s.returnURL + "?token=" + token,
		FailURL:            s.failURL + "?token=" + token,
		Email:              prep.email,
		Phone:              prep.phone,
		SessionTimeoutSecs: int(sessionTimeout.Seconds()),
		OrderBundle:        bundle,
	}
	resp, err := s.sber.Register(ctx, regReq)
	if err != nil {
		if sber.IsAlreadyProcessed(err) {
			// Этот orderNumber уже зарегистрирован — достаём заказ по orderNumber.
			return s.recoverAlreadyProcessed(ctx, prep.paymentID, prep.orderID, prep.orderNumber)
		}
		return PayResult{}, fmt.Errorf("payments: register.do: %w", err)
	}

	if err := s.markRegistered(ctx, prep.paymentID, prep.orderID, resp.OrderID, resp.FormURL); err != nil {
		return PayResult{}, err
	}
	return PayResult{FormURL: resp.FormURL}, nil
}

// recoverAlreadyProcessed достаёт статус заказа по orderNumber, когда Сбер ответил
// «уже обработан», и применяет его к платежу.
func (s *Service) recoverAlreadyProcessed(ctx context.Context, paymentID, orderID int64, orderNumber string) (PayResult, error) {
	resp, err := s.sber.GetOrderStatusExtended(ctx, sber.StatusRequest{OrderNumber: orderNumber})
	if err != nil {
		return PayResult{}, fmt.Errorf("payments: статус по orderNumber: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PayResult{}, err
	}
	defer tx.Rollback(ctx)
	var (
		status    string
		amountKop int64
	)
	if err := tx.QueryRow(ctx, `SELECT status, amount_kop FROM payments WHERE id = $1 FOR UPDATE`, paymentID).Scan(&status, &amountKop); err != nil {
		return PayResult{}, err
	}
	if !isTerminal(status) {
		if err := s.applyLocked(ctx, tx, paymentID, orderID, amountKop, resp); err != nil {
			return PayResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PayResult{}, err
	}
	// Формы у нас нет (её отдаёт только register.do). Сообщаем, что форму не получить.
	return PayResult{}, fmt.Errorf("payments: заказ %s уже зарегистрирован, форма недоступна", orderNumber)
}

// SyncPayment перепроверяет статус платежа в Сбере и применяет его к платежу и заказу.
// Идемпотентна: повторный вызов ничего не ломает.
func (s *Service) SyncPayment(ctx context.Context, paymentID int64) error {
	return s.syncInTx(ctx, paymentID, false)
}

// syncInTx: сначала читаем платёж без блокировки, идём в Сбер вне транзакции, и только
// потом открываем транзакцию, блокируем платёж, перепроверяем, что он ещё не в финальном
// статусе, и применяем ответ. skipLocked=true пропускает платёж, если его уже обрабатывает
// другой процесс (для фоновой сверки, чтобы не делать двойную работу).
func (s *Service) syncInTx(ctx context.Context, paymentID int64, skipLocked bool) error {
	// 1. Читаем платёж без блокировки.
	var (
		orderID                      int64
		sberOrderID, sberOrderNumber string
		status                       string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT order_id, sber_order_id, sber_order_number, status FROM payments WHERE id = $1`, paymentID).
		Scan(&orderID, &sberOrderID, &sberOrderNumber, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("payments: платёж %d не найден", paymentID)
	}
	if err != nil {
		return err
	}
	if isTerminal(status) {
		return nil
	}

	// 2. Запрос в Сбер вне транзакции (не держим блокировку во время сети).
	req := sber.StatusRequest{}
	if sberOrderID != "" {
		req.OrderID = sberOrderID
	} else {
		req.OrderNumber = sberOrderNumber
	}
	resp, err := s.sber.GetOrderStatusExtended(ctx, req)
	if err != nil {
		return fmt.Errorf("payments: getOrderStatusExtended: %w", err)
	}

	// 3. Транзакция: блокируем платёж, перепроверяем статус, применяем.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	q := `SELECT amount_kop, status FROM payments WHERE id = $1 FOR UPDATE`
	if skipLocked {
		q += " SKIP LOCKED"
	}
	var amountKop int64
	err = tx.QueryRow(ctx, q, paymentID).Scan(&amountKop, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		if skipLocked {
			return nil // платёж занят другим процессом — пропускаем
		}
		return fmt.Errorf("payments: платёж %d не найден", paymentID)
	}
	if err != nil {
		return err
	}
	if isTerminal(status) {
		return tx.Commit(ctx)
	}

	if err := s.applyLocked(ctx, tx, paymentID, orderID, amountKop, resp); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applyLocked обновляет платёж по ответу Сбера и применяет оплату к заказу. Платёж должен
// быть уже заблокирован (FOR UPDATE) вызывающим кодом.
func (s *Service) applyLocked(ctx context.Context, tx pgx.Tx, paymentID, orderID, amountKop int64, resp sber.StatusResponse) error {
	paid := resp.OrderStatus != nil && *resp.OrderStatus == 2

	// Оплата есть, но сумма не сошлась — в paid не переводим, только логируем и сохраняем сырьё.
	if paid && resp.Amount != amountKop {
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET last_sber_status = $1, raw = $2, updated_at = now() WHERE id = $3`,
			resp.OrderStatus, rawJSON(resp), paymentID); err != nil {
			return err
		}
		var number int64
		_ = tx.QueryRow(ctx, `SELECT number FROM orders WHERE id = $1`, orderID).Scan(&number)
		s.log.Error("сумма оплаты не совпала с заказом",
			"order_number", number, "payment_id", paymentID, "expected_kop", amountKop, "sber_kop", resp.Amount)
		return nil
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payments SET status = $1, last_sber_status = $2, raw = $3, updated_at = now() WHERE id = $4`,
		paymentStatusFromSber(resp.OrderStatus), resp.OrderStatus, rawJSON(resp), paymentID); err != nil {
		return fmt.Errorf("payments: обновление платежа: %w", err)
	}

	if !paid {
		return nil
	}

	// Оплачено — применяем к заказу.
	var (
		orderStatus string
		number      int64
	)
	if err := tx.QueryRow(ctx, `SELECT status, number FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&orderStatus, &number); err != nil {
		return err
	}
	switch orderStatus {
	case orders.StatusAwaitingPayment:
		return orders.Transition(ctx, tx, orderID, orders.StatusPaid, orders.ActorSystem, "оплачено онлайн")
	case orders.StatusCreated:
		// register прошёл, а markRegistered не успел — доводим created → awaiting_payment → paid.
		if err := orders.Transition(ctx, tx, orderID, orders.StatusAwaitingPayment, orders.ActorSystem, "оплата подтверждена"); err != nil {
			return err
		}
		return orders.Transition(ctx, tx, orderID, orders.StatusPaid, orders.ActorSystem, "оплачено онлайн")
	case orders.StatusPaid:
		return nil // уже оплачен — идемпотентно
	default:
		// Заказ уже отменён (или иной финальный статус), а оплата пришла — нужен возврат.
		if _, err := tx.Exec(ctx, `UPDATE payments SET needs_refund = true WHERE id = $1`, paymentID); err != nil {
			return err
		}
		s.log.Error("оплата пришла после отмены заказа — требуется возврат",
			"order_number", number, "order_status", orderStatus, "payment_id", paymentID)
		return nil
	}
}

// paymentStatusFromSber переводит orderStatus Сбера в статус платежа.
func paymentStatusFromSber(orderStatus *int) string {
	if orderStatus == nil {
		return StatusRegistered
	}
	switch *orderStatus {
	case 2:
		return StatusPaid
	case 6:
		return StatusDeclined
	case 3:
		return StatusReversed
	case 4:
		return StatusRefunded
	default: // 0, 1, 5 — ещё ждём
		return StatusRegistered
	}
}

func isTerminal(status string) bool {
	switch status {
	case StatusPaid, StatusDeclined, StatusReversed, StatusRefunded, StatusExpired:
		return true
	}
	return false
}
