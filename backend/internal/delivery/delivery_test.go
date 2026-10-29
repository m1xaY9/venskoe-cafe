package delivery

import (
	"context"
	"testing"
	"time"
)

// две пересекающиеся зоны-заглушки: «Центр» (sort 10) внутри «Города» (sort 20).
func testZones() []Zone {
	free := int64(150000)
	return []Zone{
		{
			ID: 1, Name: "Центр", Sort: 10,
			DeliveryPriceKop: 20000, MinOrderKop: 80000, FreeFromKop: &free,
			Polygon: Polygon{Ring: []Point{
				{Lon: 113.479, Lat: 52.014}, {Lon: 113.519, Lat: 52.014},
				{Lon: 113.519, Lat: 52.054}, {Lon: 113.479, Lat: 52.054},
			}},
		},
		{
			ID: 2, Name: "Город", Sort: 20,
			DeliveryPriceKop: 35000, MinOrderKop: 100000, FreeFromKop: nil,
			Polygon: Polygon{Ring: []Point{
				{Lon: 113.449, Lat: 51.984}, {Lon: 113.549, Lat: 51.984},
				{Lon: 113.549, Lat: 52.084}, {Lon: 113.449, Lat: 52.084},
			}},
		},
	}
}

func TestFindZonePrefersSmallerSort(t *testing.T) {
	zones := testZones()
	// Точка в центре попадает в обе зоны — должны выбрать «Центр» (sort меньше).
	z, ok := findZone(zones, Point{Lon: 113.499, Lat: 52.034})
	if !ok || z.Name != "Центр" {
		t.Fatalf("ждали зону Центр, получили %q (найдена=%v)", z.Name, ok)
	}

	// Точка только в большой зоне.
	z, ok = findZone(zones, Point{Lon: 113.46, Lat: 52.0})
	if !ok || z.Name != "Город" {
		t.Fatalf("ждали зону Город, получили %q (найдена=%v)", z.Name, ok)
	}

	// Точка вне всех зон.
	if _, ok := findZone(zones, Point{Lon: 113.0, Lat: 52.0}); ok {
		t.Error("точка вне зон не должна находиться")
	}
}

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in   string
		want int
		bad  bool
	}{
		{"10:00", 600, false},
		{"21:30", 1290, false},
		{"00:00", 0, false},
		{"23:59", 1439, false},
		{"25:00", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := parseHHMM(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("parseHHMM(%q): ждали ошибку", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseHHMM(%q) = %d, %v; ждали %d", tc.in, got, err, tc.want)
		}
	}
}

func TestOpenNow(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, loc) // конкретный день
	wd := day.Weekday()
	open := map[time.Weekday]DayHours{
		wd: {Weekday: int(wd), IsOpen: true, OpenAt: "10:00", CloseAt: "22:00"},
	}
	closed := map[time.Weekday]DayHours{
		wd: {Weekday: int(wd), IsOpen: false, OpenAt: "10:00", CloseAt: "22:00"},
	}

	at := func(h, m int) time.Time { return time.Date(2026, 10, 26, h, m, 0, 0, loc) }

	cases := []struct {
		name     string
		now      time.Time
		hours    map[time.Weekday]DayHours
		lastMin  int
		wantOpen bool
	}{
		{"днём открыто", at(12, 0), open, 30, true},
		{"ровно в открытие", at(10, 0), open, 0, true},
		{"до открытия", at(9, 59), open, 0, false},
		{"за минуту до стоп-приёма", at(21, 29), open, 30, true},
		{"ровно в стоп-приём", at(21, 30), open, 30, false},
		{"после закрытия", at(22, 30), open, 0, false},
		{"выходной день", at(12, 0), closed, 0, false},
		{"нет часов на день", at(12, 0), map[time.Weekday]DayHours{}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openNow(tc.now, tc.hours, tc.lastMin); got != tc.wantOpen {
				t.Errorf("openNow = %v, ждали %v", got, tc.wantOpen)
			}
		})
	}
}

func TestCalcDelivery(t *testing.T) {
	free := int64(150000)
	center := Zone{DeliveryPriceKop: 20000, MinOrderKop: 80000, FreeFromKop: &free}
	city := Zone{DeliveryPriceKop: 35000, MinOrderKop: 100000, FreeFromKop: nil}

	cases := []struct {
		name         string
		zone         Zone
		subtotal     int64
		wantPrice    int64
		wantFree     bool
		wantMeets    bool
		wantShortfal int64
	}{
		{"меньше минимума", center, 50000, 20000, false, false, 30000},
		{"ровно минимум", center, 80000, 20000, false, true, 0},
		{"бесплатная доставка", center, 150000, 0, true, true, 0},
		{"зона без бесплатной", city, 200000, 35000, false, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			price, free, meets, shortfall := calcDelivery(tc.zone, tc.subtotal)
			if price != tc.wantPrice || free != tc.wantFree || meets != tc.wantMeets || shortfall != tc.wantShortfal {
				t.Errorf("calcDelivery = (%d, %v, %v, %d); ждали (%d, %v, %v, %d)",
					price, free, meets, shortfall, tc.wantPrice, tc.wantFree, tc.wantMeets, tc.wantShortfal)
			}
		})
	}
}

// fakeStore — заглушка данных доставки для тестов Service.
type fakeStore struct {
	settings Settings
	hours    []DayHours
	zones    []Zone
	err      error
}

func (f fakeStore) Settings(context.Context) (Settings, error)    { return f.settings, f.err }
func (f fakeStore) WorkHours(context.Context) ([]DayHours, error) { return f.hours, f.err }
func (f fakeStore) Zones(context.Context) ([]Zone, error)         { return f.zones, f.err }

func TestServiceQuote(t *testing.T) {
	loc := time.UTC
	wd := time.Date(2026, 10, 26, 0, 0, 0, 0, loc).Weekday()
	store := fakeStore{
		settings: Settings{DeliveryEnabled: true, LastOrderBeforeCloseMin: 30},
		hours:    []DayHours{{Weekday: int(wd), IsOpen: true, OpenAt: "10:00", CloseAt: "22:00"}},
		zones:    testZones(),
	}
	svc := NewService(store, loc)

	openNowTime := time.Date(2026, 10, 26, 12, 0, 0, 0, loc)
	closedTime := time.Date(2026, 10, 26, 23, 30, 0, 0, loc)
	center := Point{Lon: 113.499, Lat: 52.034}

	t.Run("можно заказать", func(t *testing.T) {
		q, err := svc.Quote(context.Background(), openNowTime, QuoteInput{Lat: center.Lat, Lon: center.Lon, SubtotalKop: 100000})
		if err != nil {
			t.Fatal(err)
		}
		if !q.CanOrder || q.Zone != "Центр" || q.Reason != "" || q.DeliveryPriceKop != 20000 {
			t.Fatalf("неожиданный ответ: %+v", q)
		}
	})

	t.Run("мало до минимума", func(t *testing.T) {
		q, _ := svc.Quote(context.Background(), openNowTime, QuoteInput{Lat: center.Lat, Lon: center.Lon, SubtotalKop: 50000})
		if q.CanOrder || q.Reason != ReasonBelowMinimum || q.ShortfallKop != 30000 {
			t.Fatalf("ждали below_minimum: %+v", q)
		}
	})

	t.Run("закрыто", func(t *testing.T) {
		q, _ := svc.Quote(context.Background(), closedTime, QuoteInput{Lat: center.Lat, Lon: center.Lon, SubtotalKop: 100000})
		if q.CanOrder || q.Reason != ReasonClosed {
			t.Fatalf("ждали closed: %+v", q)
		}
	})

	t.Run("вне зоны", func(t *testing.T) {
		q, _ := svc.Quote(context.Background(), openNowTime, QuoteInput{Lat: 52.0, Lon: 113.0, SubtotalKop: 100000})
		if q.InZone || q.Reason != ReasonOutsideZone {
			t.Fatalf("ждали outside_zone: %+v", q)
		}
	})
}

func TestServiceQuoteDeliveryDisabled(t *testing.T) {
	loc := time.UTC
	wd := time.Date(2026, 10, 26, 0, 0, 0, 0, loc).Weekday()
	store := fakeStore{
		settings: Settings{DeliveryEnabled: false, LastOrderBeforeCloseMin: 30},
		hours:    []DayHours{{Weekday: int(wd), IsOpen: true, OpenAt: "10:00", CloseAt: "22:00"}},
		zones:    testZones(),
	}
	svc := NewService(store, loc)

	// Даже в зоне, открыто и выше минимума — заказать нельзя, если доставка выключена.
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, loc)
	q, err := svc.Quote(context.Background(), now, QuoteInput{Lat: 52.034, Lon: 113.499, SubtotalKop: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if q.CanOrder || q.Reason != ReasonDeliveryDisabled {
		t.Fatalf("ждали delivery_disabled: %+v", q)
	}
}

// Проверяем, что часы считаются в Asia/Chita: 03:00 UTC = 12:00 в Чите → открыто.
func TestServiceQuoteUsesChitaTimezone(t *testing.T) {
	chita, err := time.LoadLocation("Asia/Chita")
	if err != nil {
		t.Skipf("нет часового пояса Asia/Chita: %v", err)
	}
	nowUTC := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	wd := nowUTC.In(chita).Weekday()
	store := fakeStore{
		settings: Settings{DeliveryEnabled: true},
		hours:    []DayHours{{Weekday: int(wd), IsOpen: true, OpenAt: "10:00", CloseAt: "22:00"}},
		zones:    testZones(),
	}
	svc := NewService(store, chita)

	q, err := svc.Quote(context.Background(), nowUTC, QuoteInput{Lat: 52.034, Lon: 113.499, SubtotalKop: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if !q.OpenNow {
		t.Errorf("в 12:00 по Чите должно быть открыто: %+v", q)
	}
}

func TestServiceInfo(t *testing.T) {
	loc := time.UTC
	wd := time.Date(2026, 10, 26, 0, 0, 0, 0, loc).Weekday()
	store := fakeStore{
		settings: Settings{DeliveryEnabled: true, PickupEnabled: true, PickupAddress: "Чита, ул. Ленина, 1", PickupMinOrderKop: 50000},
		hours:    []DayHours{{Weekday: int(wd), IsOpen: true, OpenAt: "10:00", CloseAt: "22:00"}},
		zones:    testZones(),
	}
	svc := NewService(store, loc)

	info, err := svc.Info(context.Background(), time.Date(2026, 10, 26, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if !info.OpenNow || info.PickupAddress != "Чита, ул. Ленина, 1" {
		t.Fatalf("неожиданный info: %+v", info)
	}
	if len(info.WorkHours) != 1 || len(info.Zones) != 2 {
		t.Fatalf("часы/зоны: %+v", info)
	}
	if info.Zones[0].Name != "Центр" || info.Zones[0].FreeFromKop == nil {
		t.Errorf("первая зона: %+v", info.Zones[0])
	}
}
