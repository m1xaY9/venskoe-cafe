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
// позицией. Реквизиты (ФФД, СНО, ИНН, ставка НДС) берутся из настроек.
func (c *Client) BuildOrderBundle(items []BundleItem, deliveryKop int64) *OrderBundle {
	cartItems := make([]CartItem, 0, len(items)+1)
	pos := 1
	for _, it := range items {
		cartItems = append(cartItems, CartItem{
			PositionID:      strconv.Itoa(pos),
			Name:            it.Name,
			Quantity:        Quantity{Value: float64(it.Qty)},
			ItemPrice:       it.PriceKop,
			ItemAmount:      it.PriceKop * int64(it.Qty),
			MeasurementUnit: "шт.",
			PaymentMethod:   "full_payment",
			PaymentObject:   "commodity",
			Tax:             Tax{TaxType: c.fiscal.VAT},
		})
		pos++
	}
	if deliveryKop > 0 {
		cartItems = append(cartItems, CartItem{
			PositionID:      strconv.Itoa(pos),
			Name:            "Доставка",
			Quantity:        Quantity{Value: 1},
			ItemPrice:       deliveryKop,
			ItemAmount:      deliveryKop,
			MeasurementUnit: "усл.",
			PaymentMethod:   "full_payment",
			PaymentObject:   "service",
			Tax:             Tax{TaxType: c.fiscal.VAT},
		})
	}

	var company *Company
	if c.fiscal.TaxSystem != "" || c.fiscal.INN != "" {
		company = &Company{SNO: c.fiscal.TaxSystem, INN: c.fiscal.INN}
	}

	return &OrderBundle{
		FFDVersion:  c.fiscal.FFDVersion,
		ReceiptType: "sell",
		Company:     company,
		CartItems:   CartItems{Items: cartItems},
	}
}
