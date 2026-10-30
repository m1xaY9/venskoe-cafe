package delivery

import (
	"context"
	"time"
)

// Коды причин, почему заказать нельзя. Латиницей — для программы на фронте.
const (
	ReasonDeliveryDisabled = "delivery_disabled" // доставка временно отключена в настройках
	ReasonPickupDisabled   = "pickup_disabled"   // самовывоз временно отключён в настройках
	ReasonOutsideZone      = "outside_zone"      // адрес вне зон доставки
	ReasonClosed           = "closed"            // кафе сейчас закрыто или приём заказов уже окончен
	ReasonBelowMinimum     = "below_minimum"     // сумма меньше минимальной для зоны
)

// Fulfillment — способ получения заказа.
const (
	FulfillmentDelivery = "delivery"
	FulfillmentPickup   = "pickup"
)

// Settings — настройки кафе (одна строка в cafe_settings).
type Settings struct {
	DeliveryEnabled         bool   `db:"delivery_enabled"`
	PickupEnabled           bool   `db:"pickup_enabled"`
	PickupAddress           string `db:"pickup_address"`
	PickupMinOrderKop       int64  `db:"pickup_min_order_kop"`
	LastOrderBeforeCloseMin int    `db:"last_order_before_close_min"`
}

// DayHours — часы работы на один день недели. Weekday как в time.Weekday (0 — вс).
type DayHours struct {
	Weekday int    `db:"weekday"`
	IsOpen  bool   `db:"is_open"`
	OpenAt  string `db:"open_at"`  // "ЧЧ:ММ"
	CloseAt string `db:"close_at"` // "ЧЧ:ММ"
}

// Zone — зона доставки с границей-полигоном.
type Zone struct {
	ID               int64
	Name             string
	Polygon          Polygon
	DeliveryPriceKop int64
	MinOrderKop      int64
	FreeFromKop      *int64 // nil — бесплатной доставки в зоне нет
	Sort             int
}

// QuoteInput — что присылает фронт для расчёта доставки.
type QuoteInput struct {
	Lat         float64
	Lon         float64
	SubtotalKop int64
}

// Quote — ответ на расчёт доставки.
type Quote struct {
	InZone           bool   `json:"in_zone"`
	Zone             string `json:"zone,omitempty"`
	DeliveryPriceKop int64  `json:"delivery_price_kop"`
	FreeDelivery     bool   `json:"free_delivery"`
	MinOrderKop      int64  `json:"min_order_kop"`
	MeetsMinimum     bool   `json:"meets_minimum"`
	ShortfallKop     int64  `json:"shortfall_kop"`
	OpenNow          bool   `json:"open_now"`
	CanOrder         bool   `json:"can_order"`
	Reason           string `json:"reason,omitempty"`
}

// Info — данные для страницы «Доставка».
type Info struct {
	DeliveryEnabled   bool       `json:"delivery_enabled"`
	PickupEnabled     bool       `json:"pickup_enabled"`
	PickupAddress     string     `json:"pickup_address"`
	PickupMinOrderKop int64      `json:"pickup_min_order_kop"`
	OpenNow           bool       `json:"open_now"`
	WorkHours         []InfoDay  `json:"work_hours"`
	Zones             []InfoZone `json:"zones"`
}

type InfoDay struct {
	Weekday int    `json:"weekday"`
	IsOpen  bool   `json:"is_open"`
	Open    string `json:"open"`
	Close   string `json:"close"`
}

type InfoZone struct {
	Name             string `json:"name"`
	DeliveryPriceKop int64  `json:"delivery_price_kop"`
	MinOrderKop      int64  `json:"min_order_kop"`
	FreeFromKop      *int64 `json:"free_from_kop"`
}

// Store отдаёт данные о доставке из базы. В тестах — заглушка.
type Store interface {
	Settings(ctx context.Context) (Settings, error)
	WorkHours(ctx context.Context) ([]DayHours, error)
	Zones(ctx context.Context) ([]Zone, error)
}

// Service — бизнес-логика доставки. Про HTTP ничего не знает.
// loc — часовой пояс, в котором считаем «открыто сейчас» (Asia/Chita).
type Service struct {
	store Store
	loc   *time.Location
}

func NewService(store Store, loc *time.Location) *Service {
	return &Service{store: store, loc: loc}
}

// Info собирает данные для страницы «Доставка».
func (s *Service) Info(ctx context.Context, now time.Time) (Info, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return Info{}, err
	}
	hours, err := s.store.WorkHours(ctx)
	if err != nil {
		return Info{}, err
	}
	zones, err := s.store.Zones(ctx)
	if err != nil {
		return Info{}, err
	}

	info := Info{
		DeliveryEnabled:   settings.DeliveryEnabled,
		PickupEnabled:     settings.PickupEnabled,
		PickupAddress:     settings.PickupAddress,
		PickupMinOrderKop: settings.PickupMinOrderKop,
		OpenNow:           openNow(now.In(s.loc), hoursByWeekday(hours), settings.LastOrderBeforeCloseMin),
		WorkHours:         make([]InfoDay, 0, len(hours)),
		Zones:             make([]InfoZone, 0, len(zones)),
	}
	for _, h := range hours {
		info.WorkHours = append(info.WorkHours, InfoDay{
			Weekday: h.Weekday, IsOpen: h.IsOpen, Open: h.OpenAt, Close: h.CloseAt,
		})
	}
	for _, z := range zones {
		info.Zones = append(info.Zones, InfoZone{
			Name: z.Name, DeliveryPriceKop: z.DeliveryPriceKop,
			MinOrderKop: z.MinOrderKop, FreeFromKop: z.FreeFromKop,
		})
	}
	return info, nil
}

// Quote считает доставку для адреса и суммы корзины и говорит, можно ли заказать.
func (s *Service) Quote(ctx context.Context, now time.Time, in QuoteInput) (Quote, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return Quote{}, err
	}
	hours, err := s.store.WorkHours(ctx)
	if err != nil {
		return Quote{}, err
	}
	zones, err := s.store.Zones(ctx)
	if err != nil {
		return Quote{}, err
	}

	open := openNow(now.In(s.loc), hoursByWeekday(hours), settings.LastOrderBeforeCloseMin)

	// Доставка может быть выключена целиком — тогда зона и сумма уже не важны.
	if !settings.DeliveryEnabled {
		return Quote{OpenNow: open, CanOrder: false, Reason: ReasonDeliveryDisabled}, nil
	}

	zone, ok := findZone(zones, Point{Lon: in.Lon, Lat: in.Lat})
	if !ok {
		return Quote{InZone: false, OpenNow: open, CanOrder: false, Reason: ReasonOutsideZone}, nil
	}

	price, free, meets, shortfall := calcDelivery(zone, in.SubtotalKop)
	q := Quote{
		InZone:           true,
		Zone:             zone.Name,
		DeliveryPriceKop: price,
		FreeDelivery:     free,
		MinOrderKop:      zone.MinOrderKop,
		MeetsMinimum:     meets,
		ShortfallKop:     shortfall,
		OpenNow:          open,
	}
	switch {
	case !open:
		q.Reason = ReasonClosed
	case !meets:
		q.Reason = ReasonBelowMinimum
	default:
		q.CanOrder = true
	}
	return q, nil
}

// OrderPricing — вход для расчёта доставки при оформлении заказа.
type OrderPricing struct {
	Fulfillment string // "delivery" | "pickup"
	Lat, Lon    float64
	SubtotalKop int64
}

// OrderPricingResult — что нужно заказу: сколько стоит доставка, в какой зоне и можно
// ли вообще оформить. Reason пуст, если всё в порядке.
type OrderPricingResult struct {
	DeliveryKop int64
	ZoneID      *int64 // только для доставки; nil для самовывоза или вне зоны
	ZoneName    string
	Reason      string
}

// PriceOrder считает доставку для оформления заказа и проверяет, можно ли его принять
// сейчас (включён ли способ получения, попадает ли адрес в зону, открыто ли кафе,
// набрана ли минимальная сумма). В отличие от Quote, отдаёт ID зоны для сохранения.
func (s *Service) PriceOrder(ctx context.Context, now time.Time, in OrderPricing) (OrderPricingResult, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return OrderPricingResult{}, err
	}
	hours, err := s.store.WorkHours(ctx)
	if err != nil {
		return OrderPricingResult{}, err
	}
	open := openNow(now.In(s.loc), hoursByWeekday(hours), settings.LastOrderBeforeCloseMin)

	if in.Fulfillment == FulfillmentPickup {
		switch {
		case !settings.PickupEnabled:
			return OrderPricingResult{Reason: ReasonPickupDisabled}, nil
		case !open:
			return OrderPricingResult{Reason: ReasonClosed}, nil
		case in.SubtotalKop < settings.PickupMinOrderKop:
			return OrderPricingResult{Reason: ReasonBelowMinimum}, nil
		}
		return OrderPricingResult{DeliveryKop: 0}, nil
	}

	// Доставка.
	if !settings.DeliveryEnabled {
		return OrderPricingResult{Reason: ReasonDeliveryDisabled}, nil
	}
	zones, err := s.store.Zones(ctx)
	if err != nil {
		return OrderPricingResult{}, err
	}
	zone, ok := findZone(zones, Point{Lon: in.Lon, Lat: in.Lat})
	if !ok {
		return OrderPricingResult{Reason: ReasonOutsideZone}, nil
	}
	if !open {
		return OrderPricingResult{Reason: ReasonClosed}, nil
	}
	price, _, meets, _ := calcDelivery(zone, in.SubtotalKop)
	if !meets {
		return OrderPricingResult{Reason: ReasonBelowMinimum}, nil
	}
	id := zone.ID
	return OrderPricingResult{DeliveryKop: price, ZoneID: &id, ZoneName: zone.Name}, nil
}

// OpenAt говорит, работает ли кафе в момент t (с учётом last_order_before_close_min).
// t переводится в часовой пояс кафе внутри. Нужно для заказов на конкретное время.
func (s *Service) OpenAt(ctx context.Context, t time.Time) (bool, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return false, err
	}
	hours, err := s.store.WorkHours(ctx)
	if err != nil {
		return false, err
	}
	return openNow(t.In(s.loc), hoursByWeekday(hours), settings.LastOrderBeforeCloseMin), nil
}

// findZone возвращает активную зону, в которую попал адрес. Если зон несколько,
// берём первую — zones должны прийти отсортированными по sort, поэтому это зона
// с наименьшим sort.
func findZone(zones []Zone, p Point) (Zone, bool) {
	for _, z := range zones {
		if z.Polygon.Contains(p) {
			return z, true
		}
	}
	return Zone{}, false
}

// calcDelivery считает цену доставки и хватает ли суммы до минимума зоны.
func calcDelivery(z Zone, subtotalKop int64) (priceKop int64, free, meets bool, shortfallKop int64) {
	free = z.FreeFromKop != nil && subtotalKop >= *z.FreeFromKop
	priceKop = z.DeliveryPriceKop
	if free {
		priceKop = 0
	}
	meets = subtotalKop >= z.MinOrderKop
	if !meets {
		shortfallKop = z.MinOrderKop - subtotalKop
	}
	return priceKop, free, meets, shortfallKop
}

// openNow говорит, работает ли кафе и принимает ли ещё заказы в момент now.
// now уже должен быть в нужном часовом поясе — переводит его вызывающий код.
func openNow(now time.Time, hours map[time.Weekday]DayHours, lastOrderBeforeCloseMin int) bool {
	day, ok := hours[now.Weekday()]
	if !ok || !day.IsOpen {
		return false
	}
	open, err1 := parseHHMM(day.OpenAt)
	closeAt, err2 := parseHHMM(day.CloseAt)
	if err1 != nil || err2 != nil || closeAt <= open {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	lastOrder := closeAt - lastOrderBeforeCloseMin
	return cur >= open && cur < lastOrder
}

// parseHHMM переводит "ЧЧ:ММ" в минуты от полуночи.
func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func hoursByWeekday(hours []DayHours) map[time.Weekday]DayHours {
	m := make(map[time.Weekday]DayHours, len(hours))
	for _, h := range hours {
		m[time.Weekday(h.Weekday)] = h
	}
	return m
}
