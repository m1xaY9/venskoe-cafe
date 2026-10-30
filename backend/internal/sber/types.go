package sber

// Все структуры запросов повторяют поля из docs/sber-ecom-api.yaml. Необязательные поля
// с omitempty — пустые/нулевые значения Сбер не принимает (null запрещён). Суммы в копейках.

// RegisterRequest — register.do.
type RegisterRequest struct {
	UserName           string       `json:"userName"`
	Password           string       `json:"password"`
	OrderNumber        string       `json:"orderNumber"`
	Amount             int64        `json:"amount"`
	ReturnURL          string       `json:"returnUrl"`
	FailURL            string       `json:"failUrl,omitempty"`
	Email              string       `json:"email,omitempty"`
	Phone              string       `json:"phone,omitempty"`
	SessionTimeoutSecs int          `json:"sessionTimeoutSecs,omitempty"`
	OrderBundle        *OrderBundle `json:"orderBundle,omitempty"`
}

// RegisterResponse — ответ register.do.
type RegisterResponse struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	OrderID      string `json:"orderId"`
	FormURL      string `json:"formUrl"`
}

// StatusRequest — getOrderStatusExtended.do (по orderId или orderNumber).
type StatusRequest struct {
	UserName    string `json:"userName"`
	Password    string `json:"password"`
	OrderID     string `json:"orderId,omitempty"`
	OrderNumber string `json:"orderNumber,omitempty"`
}

// StatusResponse — ответ getOrderStatusExtended.do. orderStatus — указатель: его может не
// быть, если заказ не найден.
type StatusResponse struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	OrderNumber  string `json:"orderNumber"`
	OrderStatus  *int   `json:"orderStatus"`
	Amount       int64  `json:"amount"`
	ActionCode   int    `json:"actionCode"`
}

// ReverseRequest — reverse.do (полная/частичная отмена в тот же банковский день).
type ReverseRequest struct {
	UserName    string       `json:"userName"`
	Password    string       `json:"password"`
	OrderID     string       `json:"orderId"`
	Amount      int64        `json:"amount,omitempty"` // 0 = вся сумма
	OrderBundle *OrderBundle `json:"orderBundle,omitempty"`
}

// RefundRequest — refund.do (возврат после расчётов банков).
type RefundRequest struct {
	UserName    string            `json:"userName"`
	Password    string            `json:"password"`
	OrderID     string            `json:"orderId"`
	Amount      int64             `json:"amount"`
	JSONParams  map[string]string `json:"jsonParams,omitempty"` // externalRefundId — ключ идемпотентности
	OrderBundle *OrderBundle      `json:"orderBundle,omitempty"`
}

// SimpleResponse — ответ reverse.do/refund.do.
type SimpleResponse struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

// --- orderBundle для чека 54-ФЗ ---

type OrderBundle struct {
	FFDVersion  string    `json:"ffdVersion,omitempty"`
	ReceiptType string    `json:"receiptType,omitempty"`
	Company     *Company  `json:"company,omitempty"`
	CartItems   CartItems `json:"cartItems"`
}

type Company struct {
	Email          string `json:"email,omitempty"`
	SNO            string `json:"sno,omitempty"`
	INN            string `json:"inn,omitempty"`
	PaymentAddress string `json:"paymentAddress,omitempty"`
}

type CartItems struct {
	Items []CartItem `json:"items"`
}

type CartItem struct {
	PositionID      string   `json:"positionId"`
	Name            string   `json:"name"`
	Quantity        Quantity `json:"quantity"`
	ItemPrice       int64    `json:"itemPrice"`
	ItemAmount      int64    `json:"itemAmount"`
	MeasurementUnit string   `json:"measurementUnit,omitempty"`
	PaymentMethod   string   `json:"paymentMethod,omitempty"`
	PaymentObject   string   `json:"paymentObject,omitempty"`
	Tax             Tax      `json:"tax"`
}

type Quantity struct {
	Value float64 `json:"value"`
}

type Tax struct {
	TaxType int `json:"taxType"`
}

// BundleItem — позиция заказа для построения orderBundle (без деталей БД).
type BundleItem struct {
	Name     string
	PriceKop int64
	Qty      int
}
