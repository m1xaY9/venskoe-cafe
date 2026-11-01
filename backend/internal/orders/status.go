package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Статусы заказа и его жизненный цикл.
const (
	StatusCreated         = "created"          // создан; онлайн-оплата ещё не начата
	StatusAwaitingPayment = "awaiting_payment" // ждём оплату в Сбере
	StatusPaid            = "paid"             // оплачен онлайн
	StatusAccepted        = "accepted"         // кафе приняло заказ
	StatusCooking         = "cooking"          // готовится
	StatusReady           = "ready"            // готов (самовывоз)
	StatusDelivering      = "delivering"       // курьер в пути (доставка)
	StatusDelivered       = "delivered"        // доставлен (финал)
	StatusPickedUp        = "picked_up"        // забран (финал, самовывоз)
	StatusCancelled       = "cancelled"        // отменён (финал)
)

// Актор, инициировавший смену статуса.
const (
	ActorGuest  = "guest"
	ActorStaff  = "staff"
	ActorBot    = "bot"
	ActorSystem = "system"
)

// allowedTransitions — «из какого статуса в какие можно». Любой переход, которого здесь
// нет, сервер отклоняет. Финальные статусы (delivered, picked_up, cancelled) исходящих
// переходов не имеют. Кафе может отменить заказ на любом ещё не завершённом этапе.
var allowedTransitions = map[string][]string{
	StatusCreated:         {StatusAwaitingPayment, StatusAccepted, StatusCancelled},
	StatusAwaitingPayment: {StatusPaid, StatusCancelled},
	StatusPaid:            {StatusAccepted, StatusCancelled},
	StatusAccepted:        {StatusCooking, StatusCancelled},
	StatusCooking:         {StatusReady, StatusDelivering, StatusCancelled},
	StatusReady:           {StatusPickedUp, StatusCancelled},
	StatusDelivering:      {StatusDelivered, StatusCancelled},
	StatusDelivered:       {},
	StatusPickedUp:        {},
	StatusCancelled:       {},
}

// CanTransition проверяет по таблице, разрешён ли переход from → to (без учёта способа
// получения).
func CanTransition(from, to string) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// CanTransitionFor учитывает способ получения: ready и picked_up только для самовывоза,
// delivering и delivered только для доставки.
func CanTransitionFor(from, to, fulfillment string) bool {
	if !CanTransition(from, to) {
		return false
	}
	switch to {
	case StatusReady, StatusPickedUp:
		return fulfillment == FulfillmentPickup
	case StatusDelivering, StatusDelivered:
		return fulfillment == FulfillmentDelivery
	}
	return true
}

var (
	ErrOrderNotFound       = errors.New("orders: заказ не найден")
	ErrForbiddenTransition = errors.New("orders: переход статуса запрещён")
)

// Transition меняет статус заказа в рамках переданной транзакции: блокирует строку
// заказа (FOR UPDATE), проверяет переход по таблице и пишет событие в order_events —
// всё в одной транзакции со сменой статуса.
func Transition(ctx context.Context, tx pgx.Tx, orderID int64, to, actor, reason string) error {
	var from, fulfillment string
	err := tx.QueryRow(ctx, `SELECT status, fulfillment FROM orders WHERE id = $1 FOR UPDATE`, orderID).
		Scan(&from, &fulfillment)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOrderNotFound
	}
	if err != nil {
		return fmt.Errorf("orders: чтение статуса: %w", err)
	}

	if !CanTransitionFor(from, to, fulfillment) {
		return fmt.Errorf("%w: %s → %s (%s)", ErrForbiddenTransition, from, to, fulfillment)
	}

	if _, err := tx.Exec(ctx, `UPDATE orders SET status = $1, updated_at = now() WHERE id = $2`, to, orderID); err != nil {
		return fmt.Errorf("orders: смена статуса: %w", err)
	}
	if err := insertEvent(ctx, tx, orderID, &from, to, actor, reason); err != nil {
		return err
	}
	return nil
}

// insertEvent пишет строку в order_events. from == nil только для создания заказа.
func insertEvent(ctx context.Context, tx pgx.Tx, orderID int64, from *string, to, actor, reason string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO order_events (order_id, from_status, to_status, actor, reason)
		 VALUES ($1, $2, $3, $4, $5)`,
		orderID, from, to, actor, reason)
	if err != nil {
		return fmt.Errorf("orders: запись события: %w", err)
	}
	return nil
}
