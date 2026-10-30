package sber

import (
	"errors"
	"fmt"
)

// APIError — ошибка, которую Сбер вернул в теле ответа (errorCode != 0).
type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("sber: ошибка %s: %s", e.Code, e.Message)
}

// alreadyProcessedCode — Сбер отвечает так на повторную регистрацию того же orderNumber
// («Заказ с таким номером уже обработан»).
const alreadyProcessedCode = "1"

// IsAlreadyProcessed сообщает, что заказ с таким orderNumber уже был зарегистрирован.
// В этом случае данные заказа достаём через GetOrderStatusExtended по orderNumber.
func IsAlreadyProcessed(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == alreadyProcessedCode
}

// ErrDisabled возвращается, если клиент не настроен (нет логина) — онлайн-оплата выключена.
var ErrDisabled = errors.New("sber: онлайн-оплата не настроена")
