package menu

type Dish struct {
	ID          int64  `db:"id" json:"id"`
	CategoryID  int64  `db:"category_id" json:"-"`
	Title       string `db:"title" json:"title"`
	Description string `db:"description" json:"description"`
	Portion     string `db:"portion" json:"portion"`
	PriceKop    int64  `db:"price_kop" json:"price_kop"`
	ImageURL    string `db:"image_url" json:"image_url"`
	Available   bool   `db:"is_available" json:"available"`
}

type Category struct {
	ID     int64  `db:"id" json:"id"`
	Slug   string `db:"slug" json:"slug"`
	Title  string `db:"title" json:"title"`
	Dishes []Dish `db:"-" json:"dishes"`
}

func Build(categories []Category, dishes []Dish) []Category {
	byCategory := make(map[int64][]Dish, len(categories))
	for _, d := range dishes {
		byCategory[d.CategoryID] = append(byCategory[d.CategoryID], d)
	}

	result := make([]Category, 0, len(categories))
	for _, c := range categories {
		ds := byCategory[c.ID]
		if len(ds) == 0 {
			continue
		}

		c.Dishes = ds
		result = append(result, c)
	}

	return result
}
