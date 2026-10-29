package delivery

import "testing"

func TestPolygonContains(t *testing.T) {
	// Квадрат: долгота 0..10, широта 0..10. Вершины в порядке [lon, lat].
	square := Polygon{Ring: []Point{
		{Lon: 0, Lat: 0},
		{Lon: 10, Lat: 0},
		{Lon: 10, Lat: 10},
		{Lon: 0, Lat: 10},
	}}

	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"внутри", Point{Lon: 5, Lat: 5}, true},
		{"снаружи справа", Point{Lon: 15, Lat: 5}, false},
		{"снаружи слева", Point{Lon: -1, Lat: 5}, false},
		{"снаружи снизу", Point{Lon: 5, Lat: -0.01}, false},
		{"на нижней границе", Point{Lon: 5, Lat: 0}, true},
		{"в вершине", Point{Lon: 0, Lat: 0}, true},
		{"на правой границе", Point{Lon: 10, Lat: 7}, true},
		{"у самой границы изнутри", Point{Lon: 0.0001, Lat: 5}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := square.Contains(tc.p); got != tc.want {
				t.Errorf("Contains(%v) = %v, ждали %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestPolygonContainsTooFewPoints(t *testing.T) {
	line := Polygon{Ring: []Point{{Lon: 0, Lat: 0}, {Lon: 1, Lat: 1}}}
	if line.Contains(Point{Lon: 0.5, Lat: 0.5}) {
		t.Error("полигон из двух точек не может кого-то содержать")
	}
}

func TestParsePolygon(t *testing.T) {
	raw := []byte(`{"type":"Polygon","coordinates":[[[113.48,52.01],[113.52,52.01],[113.52,52.05],[113.48,52.05],[113.48,52.01]]]}`)
	poly, err := parsePolygon(raw)
	if err != nil {
		t.Fatalf("parsePolygon: %v", err)
	}
	if len(poly.Ring) != 5 {
		t.Fatalf("вершин %d, ждали 5", len(poly.Ring))
	}
	if poly.Ring[0].Lon != 113.48 || poly.Ring[0].Lat != 52.01 {
		t.Errorf("первая вершина разобрана неверно: %+v", poly.Ring[0])
	}
}

func TestParsePolygonBad(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`не json`),
		[]byte(`{"type":"Polygon","coordinates":[]}`),
	} {
		if _, err := parsePolygon(raw); err == nil {
			t.Errorf("ждали ошибку на %q", raw)
		}
	}
}
