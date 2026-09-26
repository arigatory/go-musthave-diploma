# Гофермарт — накопительная система лояльности

HTTP API для регистрации пользователей, загрузки номеров заказов, начисления баллов
через внешнюю систему расчёта (accrual) и списания баллов. Спецификация — в [SPECIFICATION.md](SPECIFICATION.md).

## Запуск

```sh
docker compose up -d postgres
go run ./cmd/gophermart \
  -a localhost:8080 \
  -d "postgres://postgres:postgres@localhost:5432/praktikum?sslmode=disable" \
  -r http://localhost:8081
```

Система расчёта начислений для локальных экспериментов лежит в `cmd/accrual`
(например, `./cmd/accrual/accrual_darwin_arm64 -a localhost:8081 -d <DATABASE_URI>`).

### Конфигурация

Переменные окружения имеют приоритет над флагами. Невалидное значение числовой
переменной или длительности (например, `POLL_INTERVAL=abc`) — ошибка на старте.

| Флаг | Переменная | Описание | По умолчанию |
|------|------------|----------|--------------|
| `-a` | `RUN_ADDRESS` | адрес и порт сервиса | `localhost:8080` |
| `-d` | `DATABASE_URI` | строка подключения к PostgreSQL | — (обязательно) |
| `-r` | `ACCRUAL_SYSTEM_ADDRESS` | адрес системы расчёта начислений | — |
| `-s` | `JWT_SECRET` | ключ подписи токенов | dev-значение |
| `-l` | `LOG_LEVEL` | уровень логирования | `info` |
| `-t` | `TOKEN_TTL` | время жизни токена | `24h` |
| `-w` | `ACCRUAL_WORKERS` | число параллельных запросов к accrual | `4` |
| `-p` | `POLL_INTERVAL` | интервал опроса accrual | `1s` |

## Устройство

```
cmd/gophermart          точка входа
internal/app            сборка зависимостей, graceful shutdown
internal/config         флаги и переменные окружения
internal/handler        HTTP-хендлеры (net/http ServeMux), логирование, gzip, recover
internal/auth           JWT, middleware аутентификации
internal/password       хеширование паролей (bcrypt)
internal/service        бизнес-логика
internal/storage/postgres  PostgreSQL (pgx), миграции (golang-migrate, embed)
internal/accrual        клиент системы начислений и фоновый воркер
internal/ratelimit      пауза запросов к внешней системе после 429
internal/model          доменные сущности и ошибки
internal/luhn           проверка номеров алгоритмом Луна
```

- Аутентификация: JWT выдаётся при регистрации/логине в заголовке `Authorization: Bearer …`
  и в cookie `token`; принимается любой из двух способов.
- Баллы хранятся в сотых долях (`BIGINT`), в JSON — десятичным числом, чтобы избежать ошибок округления float.
- Таблицы лежат в схеме `gophermart`, т. к. accrual в автотестах использует ту же базу.
- Воркер опрашивает accrual через `errgroup.SetLimit`; при `429` все запросы приостанавливаются на `Retry-After`.
  Начисление баллов и смена статуса заказа выполняются в одной транзакции и только из нефинального
  статуса, поэтому баллы не начисляются дважды. Списание блокирует строку пользователя (`FOR UPDATE`),
  и баланс не может уйти в минус.

## Тесты

```sh
go test -race -cover ./...                 # юнит-тесты
go test -tags integration -race ./...      # + интеграционные (нужен Docker, testcontainers)
go generate ./...                          # перегенерировать моки (mockgen)
```

## Обновление шаблона

```
git remote add -m master template https://github.com/yandex-praktikum/go-musthave-diploma-tpl.git
git fetch template && git checkout template/master .github
```
