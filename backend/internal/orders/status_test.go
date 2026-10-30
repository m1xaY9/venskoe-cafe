package orders

import "testing"

func TestCanTransitionAllowed(t *testing.T) {
	// Все переходы из таблицы должны быть разрешены.
	for from, tos := range allowedTransitions {
		for _, to := range tos {
			if !CanTransition(from, to) {
				t.Errorf("переход %s → %s должен быть разрешён", from, to)
			}
		}
	}
}

func TestCanTransitionForbidden(t *testing.T) {
	forbidden := [][2]string{
		{StatusCreated, StatusPaid},             // нельзя оплатить, минуя awaiting_payment
		{StatusCreated, StatusCooking},          // нельзя готовить непринятый заказ
		{StatusAwaitingPayment, StatusAccepted}, // сначала оплата
		{StatusPaid, StatusDelivered},           // «доставлен» сразу после «оплачен»
		{StatusAccepted, StatusDelivering},      // сначала cooking
		{StatusCooking, StatusDelivered},        // пропущен delivering
		{StatusReady, StatusDelivering},         // ready — это самовывоз, не доставка
		{StatusDelivering, StatusPickedUp},      // picked_up — это самовывоз
		{StatusDelivered, StatusCancelled},      // финальный статус не меняется
		{StatusPickedUp, StatusAccepted},        // финальный статус не меняется
		{StatusCancelled, StatusAccepted},       // отменённый заказ не воскрешаем
	}
	for _, p := range forbidden {
		if CanTransition(p[0], p[1]) {
			t.Errorf("переход %s → %s должен быть запрещён", p[0], p[1])
		}
	}
}

func TestDeliveryAndPickupPaths(t *testing.T) {
	// Путь доставки: cooking → delivering → delivered.
	if !CanTransition(StatusCooking, StatusDelivering) || !CanTransition(StatusDelivering, StatusDelivered) {
		t.Error("путь доставки cooking → delivering → delivered должен быть разрешён")
	}
	// Путь самовывоза: cooking → ready → picked_up.
	if !CanTransition(StatusCooking, StatusReady) || !CanTransition(StatusReady, StatusPickedUp) {
		t.Error("путь самовывоза cooking → ready → picked_up должен быть разрешён")
	}
	// Оплата при получении: created → accepted сразу.
	if !CanTransition(StatusCreated, StatusAccepted) {
		t.Error("created → accepted (оплата при получении) должен быть разрешён")
	}
}
