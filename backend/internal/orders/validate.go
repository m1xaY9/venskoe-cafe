package orders

import (
	"net/mail"
	"strings"
)

// FieldError — одна ошибка валидации. Code — латиницей для фронта, Field — какое поле,
// DishID заполняется для ошибок по конкретному блюду (например dish_unavailable).
type FieldError struct {
	Code   string `json:"code"`
	Field  string `json:"field,omitempty"`
	DishID *int64 `json:"dish_id,omitempty"`
}

// ValidationErrors — набор ошибок валидации заказа. Возвращается из Create, обработчик
// превращает его в ответ 422.
type ValidationErrors []FieldError

func (e ValidationErrors) Error() string {
	codes := make([]string, 0, len(e))
	for _, fe := range e {
		codes = append(codes, fe.Code)
	}
	return "orders: ошибки валидации: " + strings.Join(codes, ", ")
}

func (e *ValidationErrors) add(code, field string) {
	*e = append(*e, FieldError{Code: code, Field: field})
}

func (e *ValidationErrors) addDish(code string, dishID int64) {
	id := dishID
	*e = append(*e, FieldError{Code: code, DishID: &id})
}

// normalizePhone приводит телефон к виду +7XXXXXXXXXX. Принимает запись с 8 или 7 в
// начале, с пробелами, скобками и дефисами. Возвращает false, если это не похоже на
// российский мобильный из 11 цифр.
func normalizePhone(raw string) (string, bool) {
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	switch {
	case len(d) == 11 && (d[0] == '8' || d[0] == '7'):
		d = "7" + d[1:]
	case len(d) == 10:
		d = "7" + d
	default:
		return "", false
	}
	return "+" + d, true
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return false
	}
	// mail.ParseAddress пропускает "Имя <a@b>"; нам нужен голый адрес.
	return addr.Address == s
}
