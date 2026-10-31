package sber

import (
	"context"
	"strconv"
)

// Register регистрирует заказ (register.do). На ошибку errorCode != 0 возвращает *APIError;
// «уже обработан» распознаётся через IsAlreadyProcessed.
func (c *Client) Register(ctx context.Context, req RegisterRequest) (RegisterResponse, error) {
	req.UserName = c.username
	req.Password = c.password

	var resp RegisterResponse
	if err := c.do(ctx, "register.do", req, &resp); err != nil {
		return resp, err
	}
	if err := apiError(resp.ErrorCode, resp.ErrorMessage); err != nil {
		return resp, err
	}
	return resp, nil
}

// GetOrderStatusExtended запрашивает статус заказа (getOrderStatusExtended.do) по orderId
// или orderNumber.
func (c *Client) GetOrderStatusExtended(ctx context.Context, req StatusRequest) (StatusResponse, error) {
	req.UserName = c.username
	req.Password = c.password

	var resp StatusResponse
	if err := c.do(ctx, "getOrderStatusExtended.do", req, &resp); err != nil {
		return resp, err
	}
	if err := apiError(resp.ErrorCode, resp.ErrorMessage); err != nil {
		return resp, err
	}
	return resp, nil
}

// Reverse отменяет оплату в тот же банковский день (reverse.do).
func (c *Client) Reverse(ctx context.Context, req ReverseRequest) (SimpleResponse, error) {
	req.UserName = c.username
	req.Password = c.password

	var resp SimpleResponse
	if err := c.do(ctx, "reverse.do", req, &resp); err != nil {
		return resp, err
	}
	if err := apiError(resp.ErrorCode, resp.ErrorMessage); err != nil {
		return resp, err
	}
	return resp, nil
}

// Refund возвращает средства после расчётов банков (refund.do).
func (c *Client) Refund(ctx context.Context, req RefundRequest) (SimpleResponse, error) {
	req.UserName = c.username
	req.Password = c.password

	var resp SimpleResponse
	if err := c.do(ctx, "refund.do", req, &resp); err != nil {
		return resp, err
	}
	if err := apiError(resp.ErrorCode, resp.ErrorMessage); err != nil {
		return resp, err
	}
	return resp, nil
}

// apiError превращает errorCode != "0"/"" в *APIError.
func apiError(code, message string) error {
	if code == "" || code == "0" {
		return nil
	}
	return &APIError{Code: code, Message: message}
}

// BuildOrderBundle собирает корзину для чека 54-ФЗ: позиции заказа и доставку отдельной
// позицией. Реквизиты (ФФД, СНО, ИНН, ставка НДС) берутся из настроек. Если реквизиты не
// заданы (локальная разработка) — возвращает nil и пишет Warn: чек не формируем.
func (c *Client) BuildOrderBundle(items []BundleItem, deliveryKop int64) *OrderBundle {
	if !c.fiscalReady {
		c.log.Warn("orderBundle не передаётся: не заданы реквизиты чека 54-ФЗ (SBER_FFD_VERSION/TAX_SYSTEM/VAT/INN)")
		return nil
	}

	v12 := c.fiscal.FFDVersion == "1.2"
	cartItems := make([]CartItem, 0, len(items)+1)
	pos := 1
	for _, it := range items {
		cartItems = append(cartItems, c.cartItem(pos, it.ItemCode, it.Name, it.PriceKop, it.Qty, v12, false))
		pos++
	}
	if deliveryKop > 0 {
		cartItems = append(cartItems, c.cartItem(pos, "delivery", "Доставка", deliveryKop, 1, v12, true))
	}

	return &OrderBundle{
		FFDVersion:  c.fiscal.FFDVersion,
		ReceiptType: "sell",
		Company:     &Company{SNO: c.fiscal.TaxSystem, INN: c.fiscal.INN},
		CartItems:   CartItems{Items: cartItems},
	}
}

// cartItem собирает одну позицию чека с учётом версии ФФД. Для 1.2 — quantity.measure "0",
// без measurementUnit, paymentObject "1"/"4"; для 1.05 — measurementUnit и "commodity"/"service".
func (c *Client) cartItem(pos int, itemCode, name string, priceKop int64, qty int, v12, delivery bool) CartItem {
	item := CartItem{
		PositionID:    strconv.Itoa(pos),
		ItemCode:      itemCode,
		Name:          name,
		Quantity:      Quantity{Value: float64(qty)},
		ItemPrice:     priceKop,
		ItemAmount:    priceKop * int64(qty),
		PaymentMethod: "full_payment",
		Tax:           Tax{TaxType: c.fiscal.VAT},
	}
	if v12 {
		item.Quantity.Measure = "0"
		if delivery {
			item.PaymentObject = "4"
		} else {
			item.PaymentObject = "1"
		}
	} else {
		if delivery {
			item.MeasurementUnit = "усл."
			item.PaymentObject = "service"
		} else {
			item.MeasurementUnit = "шт."
			item.PaymentObject = "commodity"
		}
	}
	return item
}
