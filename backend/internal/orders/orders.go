package orders

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"venskoe/backend/internal/delivery"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Способ получения и способ оплаты.
const (
	FulfillmentDelivery = "delivery"
	FulfillmentPickup   = "pickup"
	PaymentOnline       = "online"
	PaymentOnDelivery   = "on_delivery"
)

// ItemInput — позиция из запроса гостя: что и сколько. Цену гость не присылает.
type ItemInput struct {
	DishID int64
	Qty    int
}

// CreateInput — данные для создания заказа.
type CreateInput struct {
	IdempotencyKey string
	Fulfillment    string
	PaymentMethod  string
	Name           string
	Phone          string
	Email          string
	Address        string
	Entrance       string
	Floor          string
	Flat           string
	CourierComment string
	Lat            *float64
	Lon            *float64
	Comment        string
	WhenAsap       bool
	WhenAt         *time.Time
	Items          []ItemInput
}

// Created — краткий результат создания заказа (ответ на POST /api/orders).
type Created struct {
	Token         string `json:"token"`
	Number        int64  `json:"number"`
	Status        string `json:"status"`
	Fulfillment   string `json:"fulfillment"`
	PaymentMethod string `json:"payment_method"`
	SubtotalKop   int64  `json:"subtotal_kop"`
	DeliveryKop   int64  `json:"delivery_kop"`
	TotalKop      int64  `json:"total_kop"`
}

// pricedItem — позиция с ценой и названием из базы (снимок) и количеством из запроса.
type pricedItem struct {
	DishID   int64
	Title    string
	PriceKop int64
	Qty      int
}

func (it pricedItem) lineTotal() int64 {
	return it.PriceKop * int64(it.Qty)
}

// subtotal считает сумму по позициям. Чистая функция — сумму заказа считает только
// сервер по ценам из базы, данные из запроса на цену не влияют.
func subtotal(items []pricedItem) int64 {
	var sum int64
	for _, it := range items {
		sum += it.lineTotal()
	}
	return sum
}

// DeliveryPricer считает доставку по адресу и сумме и проверяет часы работы. За ним
// стоит delivery.Service.
type DeliveryPricer interface {
	PriceOrder(ctx context.Context, now time.Time, in delivery.OrderPricing) (delivery.OrderPricingResult, error)
	OpenAt(ctx context.Context, t time.Time) (bool, error)
}

// Service создаёт и выдаёт заказы.
type Service struct {
	pool     *pgxpool.Pool
	delivery DeliveryPricer
	// now — источник текущего времени. По умолчанию time.Now; в тестах подменяется,
	// чтобы расчёт «открыто ли кафе» был предсказуемым.
	now func() time.Time
}

func NewService(pool *pgxpool.Pool, deliveryPricer DeliveryPricer) *Service {
	return &Service{pool: pool, delivery: deliveryPricer, now: time.Now}
}

// Create создаёт заказ одной транзакцией: проверяет блюда и адрес, считает сумму по
// ценам из базы и сохраняет заказ, позиции и первое событие. Повтор с тем же
// Idempotency-Key возвращает уже созданный заказ, а не создаёт второй.
func (s *Service) Create(ctx context.Context, in CreateInput) (Created, error) {
	verrs := validateFields(&in)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Created{}, fmt.Errorf("orders: начать транзакцию: %w", err)
	}
	defer tx.Rollback(ctx)

	// Уже есть заказ с таким ключом — отдаём его, не создаём новый.
	if existing, found, err := loadCreatedByKey(ctx, tx, in.IdempotencyKey); err != nil {
		return Created{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return Created{}, fmt.Errorf("orders: commit: %w", err)
		}
		return existing, nil
	}

	// Блюда: существуют, доступны, категория активна; qty 1..50.
	priced, dishErrs := loadDishes(ctx, tx, in.Items)
	verrs = append(verrs, dishErrs...)
	sub := subtotal(priced)

	// Время заказа. asap — считаем по текущему времени; заказ на время (when_at) должен
	// быть в окне [now+30мин, now+7дней] и попадать в часы работы того дня.
	effectiveTime := s.now()
	if !in.WhenAsap {
		switch {
		case in.WhenAt == nil || !whenTimingOK(*in.WhenAt, s.now()):
			verrs.add("invalid_when", "when_at")
		default:
			open, err := s.delivery.OpenAt(ctx, *in.WhenAt)
			if err != nil {
				return Created{}, fmt.Errorf("orders: проверка часов работы: %w", err)
			}
			if open {
				effectiveTime = *in.WhenAt
			} else {
				verrs.add("invalid_when", "when_at")
			}
		}
	}

	// Доставка/самовывоз: зона, минимум, часы работы. Считает пакет delivery.
	var deliveryKop int64
	var zoneID *int64
	if in.Fulfillment == FulfillmentDelivery || in.Fulfillment == FulfillmentPickup {
		op := delivery.OrderPricing{Fulfillment: in.Fulfillment, SubtotalKop: sub}
		if in.Lat != nil {
			op.Lat = *in.Lat
		}
		if in.Lon != nil {
			op.Lon = *in.Lon
		}
		res, err := s.delivery.PriceOrder(ctx, effectiveTime, op)
		if err != nil {
			return Created{}, fmt.Errorf("orders: расчёт доставки: %w", err)
		}
		if res.Reason != "" {
			verrs.add(res.Reason, "")
		}
		deliveryKop = res.DeliveryKop
		zoneID = res.ZoneID
	}

	if len(verrs) > 0 {
		return Created{}, verrs
	}

	total := sub + deliveryKop
	token, err := newToken()
	if err != nil {
		return Created{}, err
	}

	id, number, inserted, err := insertOrder(ctx, tx, in, token, total, deliveryKop, sub, zoneID)
	if err != nil {
		return Created{}, err
	}
	if !inserted {
		// Проиграли гонку по идемпотентности: заказ уже создан параллельным запросом.
		existing, found, err := loadCreatedByKey(ctx, tx, in.IdempotencyKey)
		if err != nil {
			return Created{}, err
		}
		if !found {
			return Created{}, fmt.Errorf("orders: конфликт идемпотентности без заказа")
		}
		if err := tx.Commit(ctx); err != nil {
			return Created{}, fmt.Errorf("orders: commit: %w", err)
		}
		return existing, nil
	}

	if err := insertItems(ctx, tx, id, priced); err != nil {
		return Created{}, err
	}
	// Первое событие — создание заказа (from_status пустой).
	if err := insertEvent(ctx, tx, id, nil, StatusCreated, ActorGuest, "заказ создан"); err != nil {
		return Created{}, err
	}

	// Оплата при получении — сразу принимаем заказ. Онлайн-оплата остаётся в created,
	// её подхватит следующая ветка.
	status := StatusCreated
	if in.PaymentMethod == PaymentOnDelivery {
		if err := Transition(ctx, tx, id, StatusAccepted, ActorSystem, "оплата при получении"); err != nil {
			return Created{}, err
		}
		status = StatusAccepted
	}

	if err := tx.Commit(ctx); err != nil {
		return Created{}, fmt.Errorf("orders: commit: %w", err)
	}

	return Created{
		Token:         token,
		Number:        number,
		Status:        status,
		Fulfillment:   in.Fulfillment,
		PaymentMethod: in.PaymentMethod,
		SubtotalKop:   sub,
		DeliveryKop:   deliveryKop,
		TotalKop:      total,
	}, nil
}

// validateFields проверяет поля запроса без обращения к базе. Побочно нормализует
// телефон в in.Phone.
func validateFields(in *CreateInput) ValidationErrors {
	var verrs ValidationErrors

	// Имя и адрес сохраняем обрезанными по пробелам.
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)

	if in.Name == "" {
		verrs.add("invalid_name", "name")
	}

	if phone, ok := normalizePhone(in.Phone); ok {
		in.Phone = phone
	} else {
		verrs.add("invalid_phone", "phone")
	}

	if in.Fulfillment != FulfillmentDelivery && in.Fulfillment != FulfillmentPickup {
		verrs.add("invalid_fulfillment", "fulfillment")
	}
	if in.PaymentMethod != PaymentOnline && in.PaymentMethod != PaymentOnDelivery {
		verrs.add("invalid_payment_method", "payment_method")
	}

	// Email обязателен для онлайн-оплаты (его требует Сбер); иначе — только если указан.
	switch {
	case in.PaymentMethod == PaymentOnline:
		if !validEmail(in.Email) {
			verrs.add("invalid_email", "email")
		}
	case in.Email != "" && !validEmail(in.Email):
		verrs.add("invalid_email", "email")
	}

	if len(in.Items) == 0 {
		verrs.add("empty_order", "items")
	}

	if in.Fulfillment == FulfillmentDelivery {
		if !validCoordinates(in.Lat, in.Lon) {
			verrs.add("invalid_coordinates", "lat")
		}
		if in.Address == "" {
			verrs.add("invalid_address", "address")
		}
	}

	return verrs
}

// whenTimingOK проверяет, что время заказа не раньше чем через 30 минут и не позже чем
// через 7 дней от now. Попадание в часы работы проверяется отдельно, через delivery.
func whenTimingOK(whenAt, now time.Time) bool {
	return !whenAt.Before(now.Add(30*time.Minute)) && !whenAt.After(now.Add(7*24*time.Hour))
}

func validCoordinates(lat, lon *float64) bool {
	if lat == nil || lon == nil {
		return false
	}
	return *lat >= -90 && *lat <= 90 && *lon >= -180 && *lon <= 180
}

// newToken — случайный public_token на 128 бит в base64url без паддинга.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("orders: генерация токена: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
