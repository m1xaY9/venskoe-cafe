# Венское кафе — бэкенд

API кафе «Венское» (Чита): меню, доставка и самовывоз, оформление заказа с расчётом суммы
на сервере и онлайн-оплата через интернет-эквайринг Сбербанка.

## Стек

- Go, стандартная библиотека `net/http` (маршруты Go 1.22), без веб-фреймворка.
- PostgreSQL 17, драйвер `jackc/pgx/v5` (SQL пишется руками, без ORM).
- Миграции `pressly/goose` (встроены в бинарник, применяются на старте).
- Конфигурация из переменных окружения (`caarlos0/env`), логи `log/slog`.

## Структура репозитория

```
backend/            Go API: меню, доставка, заказы, оплата
.github/            CI: проверки на каждый пул-реквест
docker-compose.yml  PostgreSQL для локальной разработки
```

## Локальный запуск

Нужен Go 1.25+ и Docker.

```bash
cd backend
cp .env.example .env    # настройки для локального запуска
make db-up              # поднять PostgreSQL в Docker
make run                # API на http://localhost:8080 (на старте применяет миграции)
make db-seed            # залить тестовое меню в локальную базу (после первого make run)
make test               # тесты
make lint               # форматирование и go vet
```

Проверка, что сервер жив: `curl http://localhost:8080/healthz`

## Переменные окружения

Полный список с примерами — в `backend/.env.example`.

| Переменная | Назначение |
|---|---|
| `APP_ENV` | `local` / `staging` / `production` |
| `HTTP_ADDR` | адрес прослушивания (по умолчанию `:8080`) |
| `SHUTDOWN_TIMEOUT` | таймаут корректной остановки |
| `DATABASE_URL` | строка подключения к PostgreSQL (обязательна) |
| `PUBLIC_BASE_URL` | адрес фронтенда для редиректов (страница заказа) |
| `DADATA_API_KEY` | токен DaData для подсказок адреса; без него подсказки отвечают `503` |
| `TRUSTED_PROXIES` | доверенные прокси (CIDR через запятую) для чтения `X-Forwarded-For` |
| `SBER_BASE_URL` | контур эквайринга; в `production` обязателен `https://epay.sberbank.ru` |
| `SBER_USERNAME`, `SBER_PASSWORD` | доступ к API Сбера; без логина онлайн-оплата отключена |
| `SBER_CA_FILE` | путь к корневому сертификату НУЦ Минцифры (в репозиторий не кладётся) |
| `SBER_RETURN_URL`, `SBER_FAIL_URL` | адреса возврата гостя со страницы оплаты |
| `SBER_FFD_VERSION`, `SBER_TAX_SYSTEM`, `SBER_VAT`, `SBER_INN` | реквизиты чека 54-ФЗ; в `production` при включённом Сбере обязательны |

В `production` при включённом Сбере приложение не стартует, если `SBER_BASE_URL` не на
`https://epay.sberbank.ru` или не заданы реквизиты чека. Без `SBER_USERNAME` онлайн-оплата
отключена, остальной API работает.

## API

| Метод | Что делает |
|---|---|
| `GET /healthz` | жив ли сервис |
| `GET /readyz` | доступна ли база |
| `GET /api/menu` | меню для гостя: `{"categories": [...]}`; блюда из стоп-листа отдаются с `available: false` |
| `GET /api/delivery/info` | часы работы, открыто ли сейчас, адрес самовывоза и список зон доставки |
| `POST /api/delivery/quote` | по `{lat, lon, subtotal_kop}`: зона, цена доставки, минимум, можно ли заказать и почему нет (`delivery_disabled` / `outside_zone` / `closed` / `below_minimum`). `lat`/`lon` обязательны и в допустимых пределах, иначе `422 invalid_coordinates` |
| `GET /api/address/suggest?q=...` | подсказки адреса через DaData (прокси, чтобы токен не попал в браузер); от 3 символов, до 10 подсказок |
| `POST /api/orders` | оформить заказ. Сумму считает сервер по ценам из базы. Нужен заголовок `Idempotency-Key` (UUID, иначе `400`): повтор с тем же ключом вернёт тот же заказ. Ошибки валидации — `422` с кодом по каждому полю. Тело до 64 КБ, до 50 позиций. Больше 10 заказов в минуту с одного IP — `429` |
| `GET /api/orders/{token}` | заказ для гостя по публичному токену: состав, суммы, статус и история этапов. `404`, если токена нет |
| `POST /api/orders/{token}/pay` | начать онлайн-оплату через Сбер. Возвращает `{form_url}`. Если Сбер не настроен — `503`; если заказ не онлайн/нельзя оплатить — `409` |
| `POST /api/payments/sber/callback` | уведомление от Сбера. Всегда отвечает `200`, статус перепроверяется в фоне через `getOrderStatusExtended` |
| `GET /api/payments/sber/return?token=...` | возврат гостя со страницы оплаты: перепроверяем статус и ведём на `PUBLIC_BASE_URL/order/{token}` |

### Примеры

```bash
curl http://localhost:8080/api/menu
curl http://localhost:8080/api/delivery/info
curl -X POST http://localhost:8080/api/delivery/quote -d '{"lat":52.034,"lon":113.499,"subtotal_kop":100000}'
curl "http://localhost:8080/api/address/suggest?q=Чита+Ленина"   # нужен DADATA_API_KEY

# Заказ (самовывоз, оплата при получении); dish_id берутся из /api/menu:
curl -X POST http://localhost:8080/api/orders \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"fulfillment":"pickup","payment_method":"on_delivery","name":"Иван","phone":"8 999 123-45-67","items":[{"dish_id":1,"qty":2}]}'

# Открыть заказ по токену из ответа:
curl http://localhost:8080/api/orders/<token>
```

### Проверка оплаты на тестовом контуре Сбера

Нужны тестовые логин/пароль эквайринга и корневой сертификат НУЦ Минцифры (`gosuslugi.ru/crt`).

```bash
# в backend/.env заполнить SBER_USERNAME, SBER_PASSWORD, SBER_CA_FILE, SBER_RETURN_URL, PUBLIC_BASE_URL.
make db-up && make run &
make db-seed
# создать онлайн-заказ (payment_method=online, нужен email):
curl -X POST http://localhost:8080/api/orders -H "Idempotency-Key: $(uuidgen)" \
  -d '{"fulfillment":"pickup","payment_method":"online","name":"Иван","phone":"89991234567","email":"you@example.com","items":[{"dish_id":1,"qty":1}]}'
# начать оплату (token из ответа) → получить form_url:
curl -X POST http://localhost:8080/api/orders/<token>/pay
# открыть form_url и оплатить тестовой картой МИР 2202 2050 0001 2424, 05/35, CVC 669.
curl http://localhost:8080/api/orders/<token>   # статус станет paid
```
