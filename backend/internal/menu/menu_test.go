package menu

import (
	"encoding/json"
	"testing"
)

func TestBuild(t *testing.T) {
	categories := []Category{
		{ID: 1, Slug: "zavtraki", Title: "Завтраки"},
		{ID: 2, Slug: "supy", Title: "Супы"},
		{ID: 3, Slug: "deserty", Title: "Десерты"},
	}
	dishes := []Dish{
		{ID: 10, CategoryID: 1, Title: "Сырники"},
		{ID: 11, CategoryID: 3, Title: "Захер-торт"},
		{ID: 12, CategoryID: 1, Title: "Омлет"},
	}

	got := Build(categories, dishes)

	if len(got) != 2 {
		t.Fatalf("категорий %d, ждали 2 (пустые не показываем)", len(got))
	}
	if got[0].Slug != "zavtraki" || got[1].Slug != "deserty" {
		t.Errorf("порядок категорий нарушен: %s, %s", got[0].Slug, got[1].Slug)
	}
	if len(got[0].Dishes) != 2 || got[0].Dishes[0].ID != 10 || got[0].Dishes[1].ID != 12 {
		t.Errorf("блюда завтраков: %+v", got[0].Dishes)
	}
}

func TestBuildEmptyIsJSONArray(t *testing.T) {
	b, err := json.Marshal(Build(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Errorf("получили %s, ждали []", b)
	}
}
