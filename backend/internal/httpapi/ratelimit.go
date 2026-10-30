package httpapi

import (
	"sync"
	"time"
)

// rateLimiter — простое ограничение частоты в памяти: не больше limit событий за window
// с одного ключа (IP). Для одного сервера этого достаточно; при нескольких экземплярах
// понадобится общий счётчик (например в Redis).
type rateLimiter struct {
	mu        sync.Mutex
	hits      map[string][]time.Time
	limit     int
	window    time.Duration
	lastSweep time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{hits: make(map[string][]time.Time), limit: limit, window: window}
}

// allow регистрирует попытку и говорит, в пределах ли лимита. now передаётся параметром,
// чтобы поведение можно было проверить тестом без ожидания реального времени.
func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Не чаще раза в окно чистим всю карту от пустых и устаревших ключей, чтобы память
	// не росла от разовых IP.
	if now.Sub(rl.lastSweep) >= rl.window {
		rl.sweep(now)
		rl.lastSweep = now
	}

	cutoff := now.Add(-rl.window)
	kept := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= rl.limit {
		rl.hits[key] = kept
		return false
	}
	rl.hits[key] = append(kept, now)
	return true
}

// sweep удаляет ключи, у которых не осталось свежих попыток. Вызывается под залоченным mu.
func (rl *rateLimiter) sweep(now time.Time) {
	cutoff := now.Add(-rl.window)
	for key, ts := range rl.hits {
		kept := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(rl.hits, key)
		} else {
			rl.hits[key] = kept
		}
	}
}
