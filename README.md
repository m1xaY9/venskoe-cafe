# Венское кафе: сайт с меню, доставкой и онлайн-оплатой

Собственный сайт кафе «Венское» (Чита) вместо сайта на Tilda.

Сайт рассчитан в первую очередь на телефон (mobile-first). Гость выбирает блюда, оформляет доставку или самовывоз и оплачивает онлайн через интернет-эквайринг Сбербанка (карта, SberPay, СБП) или при получении. Кафе видит заявки в админке и получает уведомления в Telegram-боте.

## Структура репозитория

```
backend/    Go API: меню, заказы, оплата, доставка, Telegram-бот
frontend/   Next.js: сайт для гостей и админка (появится отдельной веткой)
deploy/     Docker Compose, Caddy, скрипты деплоя на сервер (появится позже)
docs/       Как устроен проект и как в нём работать
.github/    CI: проверки на каждый пул-реквест, деплой из main
```

## С чего начать

1. [docs/workflow.md](docs/workflow.md): как мы работаем с git, ветками и пул-реквестами.
2. [docs/architecture.md](docs/architecture.md): из каких частей состоит система и как проходит заказ.
3. [docs/roadmap.md](docs/roadmap.md): этапы работ и что входит в каждый.

## Локальный запуск бэкенда

Нужен Go 1.24+.

```bash
cd backend
cp .env.example .env   # настройки для локального запуска
make db-up             # поднять PostgreSQL в Docker
make run               # API на http://localhost:8080 (на старте применяет миграции)
make db-seed           # залить тестовое меню в локальную базу (после первого make run)
make test              # тесты
make lint              # проверки, которые запускает CI
```

Проверка, что сервер жив: `curl http://localhost:8080/healthz`

## API

| Метод | Что делает |
|---|---|
| `GET /healthz` | жив ли сервис |
| `GET /readyz` | доступна ли база |
| `GET /api/menu` | меню для гостя: `{"categories": [...]}` с блюдами по категориям; блюда из стоп-листа отдаются с `available: false` |
| `GET /api/delivery/info` | часы работы, открыто ли сейчас, адрес самовывоза и список зон доставки — для страницы «Доставка» |
| `POST /api/delivery/quote` | по `{lat, lon, subtotal_kop}` отвечает: зона, цена доставки, минимум, можно ли заказать и почему нет (`delivery_disabled` / `outside_zone` / `closed` / `below_minimum`). `lat`/`lon` обязательны и в допустимых пределах, иначе `422 invalid_coordinates` |
| `GET /api/address/suggest?q=...` | подсказки адреса через DaData (прокси, чтобы токен не попал в браузер); от 3 символов, до 10 подсказок |
| `POST /api/orders` | оформить заказ. Сумму считает сервер по ценам из базы. Нужен заголовок `Idempotency-Key` (UUID, иначе `400`): повтор с тем же ключом вернёт тот же заказ. Ошибки валидации — `422` с кодом по каждому полю (`dish_unavailable`, `below_minimum`, `outside_zone`, `closed`, `invalid_phone`, `invalid_address`, `invalid_when`, `invalid_coordinates`, `too_long`, ...). Тело до 64 КБ, до 50 позиций. Больше 10 заказов в минуту с одного IP — `429` |
| `GET /api/orders/{token}` | заказ для гостя по публичному токену: состав, суммы, статус и история этапов. `404`, если токена нет |

Примеры:
```bash
curl http://localhost:8080/api/menu
curl http://localhost:8080/api/delivery/info
curl -X POST http://localhost:8080/api/delivery/quote -d '{"lat":52.034,"lon":113.499,"subtotal_kop":100000}'
curl "http://localhost:8080/api/address/suggest?q=Чита+Ленина"   # нужен DADATA_API_KEY в .env

# Создать заказ (самовывоз, оплата при получении). dish_id берутся из /api/menu.
curl -X POST http://localhost:8080/api/orders \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"fulfillment":"pickup","payment_method":"on_delivery","name":"Иван","phone":"8 999 123-45-67","items":[{"dish_id":1,"qty":2}]}'

# Открыть заказ по токену из ответа:
curl http://localhost:8080/api/orders/<token>
```

Подсказки адреса работают только если в `.env` задан `DADATA_API_KEY`; без него `GET /api/address/suggest` отвечает `503`, остальной сайт работает.

Если приложение стоит за обратным прокси (Caddy), укажите его адреса в `TRUSTED_PROXIES` (CIDR через запятую) — тогда IP гостя для ограничения частоты берётся из `X-Forwarded-For`.
