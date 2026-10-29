package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Point — точка на карте. Порядок как в GeoJSON: сначала долгота, потом широта.
type Point struct {
	Lon float64
	Lat float64
}

// Polygon — внешнее кольцо зоны доставки: список вершин по порядку.
// Дырки внутри зоны мы не поддерживаем, нам достаточно внешней границы.
type Polygon struct {
	Ring []Point
}

// eps — небольшой допуск для сравнения дробных координат.
const eps = 1e-9

// Contains проверяет, лежит ли точка внутри полигона, методом трассировки луча
// (ray casting): пускаем луч вправо и считаем пересечения со сторонами. Нечётное
// число пересечений — точка внутри. Точку на самой границе считаем внутренней.
func (p Polygon) Contains(pt Point) bool {
	ring := p.Ring
	n := len(ring)
	if n < 3 {
		return false
	}

	// Сначала отдельно ловим точку на границе — у трассировки луча поведение на
	// границе неопределённое, а нам нужен предсказуемый ответ.
	for i := 0; i < n; i++ {
		a := ring[i]
		b := ring[(i+1)%n]
		if onSegment(pt, a, b) {
			return true
		}
	}

	inside := false
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		a, b := ring[i], ring[j]
		if (a.Lat > pt.Lat) != (b.Lat > pt.Lat) {
			x := (b.Lon-a.Lon)*(pt.Lat-a.Lat)/(b.Lat-a.Lat) + a.Lon
			if pt.Lon < x {
				inside = !inside
			}
		}
	}
	return inside
}

// onSegment: лежит ли точка p на отрезке a—b.
func onSegment(p, a, b Point) bool {
	// Точки на одной прямой, если векторное произведение близко к нулю.
	cross := (b.Lon-a.Lon)*(p.Lat-a.Lat) - (b.Lat-a.Lat)*(p.Lon-a.Lon)
	if math.Abs(cross) > eps {
		return false
	}
	// И при этом точка в пределах прямоугольника, охватывающего отрезок.
	if p.Lon < math.Min(a.Lon, b.Lon)-eps || p.Lon > math.Max(a.Lon, b.Lon)+eps {
		return false
	}
	if p.Lat < math.Min(a.Lat, b.Lat)-eps || p.Lat > math.Max(a.Lat, b.Lat)+eps {
		return false
	}
	return true
}

// parsePolygon разбирает GeoJSON-полигон из базы. Координаты в порядке [lon, lat].
// Берём только внешнее кольцо (первый элемент coordinates).
func parsePolygon(raw []byte) (Polygon, error) {
	var gj struct {
		Type        string        `json:"type"`
		Coordinates [][][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &gj); err != nil {
		return Polygon{}, fmt.Errorf("polygon: не разобрать GeoJSON: %w", err)
	}
	if len(gj.Coordinates) == 0 {
		return Polygon{}, errors.New("polygon: пустые координаты")
	}
	outer := gj.Coordinates[0]
	ring := make([]Point, 0, len(outer))
	for _, c := range outer {
		if len(c) < 2 {
			return Polygon{}, errors.New("polygon: вершина без пары координат")
		}
		ring = append(ring, Point{Lon: c[0], Lat: c[1]})
	}
	return Polygon{Ring: ring}, nil
}
