package orders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// loadDishes берёт блюда из базы по id из запроса и собирает позиции со снимком
// названия и цены. Ошибки по недоступным блюдам и неверному qty возвращает в
// ValidationErrors; сбой самой базы — обычной error (обработчик ответит 500 и залогирует).
func loadDishes(ctx context.Context, tx pgx.Tx, items []ItemInput) ([]pricedItem, ValidationErrors, error) {
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.DishID)
	}

	type dishRow struct {
		title     string
		priceKop  int64
		available bool
		catActive bool
	}
	byID := make(map[int64]dishRow, len(ids))

	rows, err := tx.Query(ctx, `
		SELECT d.id, d.title, d.price_kop, d.is_available, c.is_active
		FROM dishes d
		JOIN categories c ON c.id = d.category_id
		WHERE d.id = ANY($1)`, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("orders: чтение блюд: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			d  dishRow
		)
		if err := rows.Scan(&id, &d.title, &d.priceKop, &d.available, &d.catActive); err != nil {
			return nil, nil, fmt.Errorf("orders: чтение блюд: %w", err)
		}
		byID[id] = d
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("orders: чтение блюд: %w", err)
	}

	var verrs ValidationErrors
	priced := make([]pricedItem, 0, len(items))
	for _, it := range items {
		if it.Qty < 1 || it.Qty > 50 {
			verrs.addDish("invalid_qty", it.DishID)
			continue
		}
		d, ok := byID[it.DishID]
		if !ok || !d.available || !d.catActive {
			verrs.addDish("dish_unavailable", it.DishID)
			continue
		}
		priced = append(priced, pricedItem{DishID: it.DishID, Title: d.title, PriceKop: d.priceKop, Qty: it.Qty})
	}
	return priced, verrs, nil
}

// insertOrder вставляет заказ. При конфликте по idempotency_key ничего не вставляет и
// возвращает inserted=false.
func insertOrder(ctx context.Context, tx pgx.Tx, in CreateInput, token string, totalKop, deliveryKop, subtotalKop int64, zoneID *int64) (id, number int64, inserted bool, err error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO orders (
			public_token, status, fulfillment, payment_method, customer_name, phone, email,
			address, entrance, floor, flat, courier_comment, lat, lon, delivery_zone_id,
			comment, when_asap, when_at, subtotal_kop, delivery_kop, total_kop, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, number`,
		token, StatusCreated, in.Fulfillment, in.PaymentMethod, in.Name, in.Phone, in.Email,
		in.Address, in.Entrance, in.Floor, in.Flat, in.CourierComment, in.Lat, in.Lon, zoneID,
		in.Comment, in.WhenAsap, in.WhenAt, subtotalKop, deliveryKop, totalKop, in.IdempotencyKey)

	err = row.Scan(&id, &number)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("orders: вставка заказа: %w", err)
	}
	return id, number, true, nil
}

func insertItems(ctx context.Context, tx pgx.Tx, orderID int64, items []pricedItem) error {
	for _, it := range items {
		_, err := tx.Exec(ctx, `
			INSERT INTO order_items (order_id, dish_id, title, price_kop, qty, line_total_kop)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			orderID, it.DishID, it.Title, it.PriceKop, it.Qty, it.lineTotal())
		if err != nil {
			return fmt.Errorf("orders: вставка позиции: %w", err)
		}
	}
	return nil
}

// loadCreatedByKey ищет заказ по ключу идемпотентности и собирает краткий ответ.
func loadCreatedByKey(ctx context.Context, tx pgx.Tx, key string) (Created, bool, error) {
	var c Created
	err := tx.QueryRow(ctx, `
		SELECT public_token, number, status, fulfillment, payment_method,
		       subtotal_kop, delivery_kop, total_kop
		FROM orders WHERE idempotency_key = $1`, key).
		Scan(&c.Token, &c.Number, &c.Status, &c.Fulfillment, &c.PaymentMethod,
			&c.SubtotalKop, &c.DeliveryKop, &c.TotalKop)
	if errors.Is(err, pgx.ErrNoRows) {
		return Created{}, false, nil
	}
	if err != nil {
		return Created{}, false, fmt.Errorf("orders: поиск по ключу: %w", err)
	}
	return c, true, nil
}

// --- Гостевой вид заказа (GET /api/orders/{token}) ---

type GuestOrder struct {
	Token         string        `json:"token"`
	Number        int64         `json:"number"`
	Status        string        `json:"status"`
	Fulfillment   string        `json:"fulfillment"`
	PaymentMethod string        `json:"payment_method"`
	Name          string        `json:"name"`
	Phone         string        `json:"phone"`
	Email         string        `json:"email,omitempty"`
	Address       *GuestAddress `json:"address,omitempty"`
	Comment       string        `json:"comment,omitempty"`
	WhenAsap      bool          `json:"when_asap"`
	WhenAt        *time.Time    `json:"when_at,omitempty"`
	Items         []GuestItem   `json:"items"`
	SubtotalKop   int64         `json:"subtotal_kop"`
	DeliveryKop   int64         `json:"delivery_kop"`
	TotalKop      int64         `json:"total_kop"`
	Events        []GuestEvent  `json:"events"`
	CreatedAt     time.Time     `json:"created_at"`
}

type GuestAddress struct {
	Address        string `json:"address"`
	Entrance       string `json:"entrance,omitempty"`
	Floor          string `json:"floor,omitempty"`
	Flat           string `json:"flat,omitempty"`
	CourierComment string `json:"courier_comment,omitempty"`
}

type GuestItem struct {
	Title        string `json:"title"`
	PriceKop     int64  `json:"price_kop"`
	Qty          int    `json:"qty"`
	LineTotalKop int64  `json:"line_total_kop"`
}

type GuestEvent struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

// ByToken отдаёт заказ гостю по публичному токену: состав, суммы, статус, адрес и
// историю этапов. Внутренних id наружу не отдаёт. ErrOrderNotFound, если токена нет.
func (s *Service) ByToken(ctx context.Context, token string) (GuestOrder, error) {
	var (
		o      GuestOrder
		id     int64
		addr   GuestAddress
		whenAt *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, public_token, number, status, fulfillment, payment_method,
		       customer_name, phone, email, address, entrance, floor, flat, courier_comment,
		       comment, when_asap, when_at, subtotal_kop, delivery_kop, total_kop, created_at
		FROM orders WHERE public_token = $1`, token).
		Scan(&id, &o.Token, &o.Number, &o.Status, &o.Fulfillment, &o.PaymentMethod,
			&o.Name, &o.Phone, &o.Email, &addr.Address, &addr.Entrance, &addr.Floor, &addr.Flat, &addr.CourierComment,
			&o.Comment, &o.WhenAsap, &whenAt, &o.SubtotalKop, &o.DeliveryKop, &o.TotalKop, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GuestOrder{}, ErrOrderNotFound
	}
	if err != nil {
		return GuestOrder{}, fmt.Errorf("orders: чтение заказа: %w", err)
	}
	o.WhenAt = whenAt
	if o.Fulfillment == FulfillmentDelivery {
		o.Address = &addr
	}

	items, err := s.loadGuestItems(ctx, id)
	if err != nil {
		return GuestOrder{}, err
	}
	o.Items = items

	events, err := s.loadGuestEvents(ctx, id)
	if err != nil {
		return GuestOrder{}, err
	}
	o.Events = events

	return o, nil
}

func (s *Service) loadGuestItems(ctx context.Context, orderID int64) ([]GuestItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT title, price_kop, qty, line_total_kop
		FROM order_items WHERE order_id = $1 ORDER BY id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: чтение позиций: %w", err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[GuestItem])
	if err != nil {
		return nil, fmt.Errorf("orders: чтение позиций: %w", err)
	}
	return items, nil
}

func (s *Service) loadGuestEvents(ctx context.Context, orderID int64) ([]GuestEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_status, created_at
		FROM order_events WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: чтение истории: %w", err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[GuestEvent])
	if err != nil {
		return nil, fmt.Errorf("orders: чтение истории: %w", err)
	}
	return events, nil
}
