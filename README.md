# Download Track Bot

Телеграм‑бот и HTTP‑сервис на Go для скачивания файлов по URL и доставки их пользователю — на email, в Telegram‑чат или оба варианта одновременно.

## Возможности

- Регистрация пользователя по команде `/register email@example.com`, генерация API‑ключа.
- Приём ссылок на файлы: бот предлагает inline‑клавиатуру с выбором способа доставки.
- Три режима доставки:
  - **На email** — файл скачивается и отправляется как вложение на зарегистрированный email (SMTP).
  - **В этот чат** — файл отправляется напрямую в Telegram‑чат через локальный Telegram Bot API (поддержка файлов до 2 ГБ).
  - **И туда, и сюда** — файл доставляется одновременно на email и в Telegram‑чат.
- Запрос смены email через `/change_email`, подтверждение/отклонение админом через `/approve_change` и `/reject_change`.
- Хранение пользователей и заявок в PostgreSQL.
- Структурированные логи доставки с указанием режима (`mode=email/telegram/both`), статусов и размера файла.

## Стек

- Go, `github.com/go-telegram-bot-api/telegram-bot-api/v5` для бота.
- Локальный [Telegram Bot API](https://github.com/tdlib/telegram-bot-api) (`tdlib/telegram-bot-api`) для доставки файлов в Telegram напрямую.
- PostgreSQL для хранения пользователей и заявок.
- Docker Compose для запуска `bot`, `http-service`, `postgres` и `telegram-bot-api`.
- SMTP‑сервер для исходящей почты.

## Архитектура

Проект состоит из двух сервисов:

- `bot` — Telegram‑бот (точка входа в `cmd/bot`), который:
  - обрабатывает команды (`/start`, `/register`, `/change_email`, `/help`, админские команды);
  - принимает ссылки от пользователя и показывает inline‑клавиатуру для выбора режима доставки;
  - делает HTTP‑запрос `POST /send` в HTTP‑сервис.
- `http-service` — HTTP‑сервис (точка входа в `cmd/http-service`), который:
  - по `api_key` находит пользователя в БД;
  - скачивает файл по URL;
  - доставляет его по email, в Telegram или одновременно (через реализацию `Delivery`).

Подробнее устройство пакетов описано в `ARCHITECTURE.md`.

## Быстрый старт

1. Скопировать `.env.example` в `.env` и заполнить все параметры.

2. Запустить сервисы:
   ```bash
   docker compose up --build -d
   ```

3. Написать боту в Telegram, выполнить `/start`, затем `/register email@example.com` и отправить ссылку на файл (можно с командой `/send <url>`, можно просто ссылкой).

4. В ответ бот предложит выбрать способ доставки через inline‑клавиатуру:

   - **На email** — файл придёт на зарегистрированный email.
   - **В этот чат** — файл придёт прямо в Telegram‑чат.
   - **И туда, и туда** — файл будет доставлен обоими способами.

## Проверка работоспособности

1. **Контейнеры и логи**
   ```bash
   docker compose ps
   docker logs filemailer-bot --tail=20
   docker logs filemailer-http --tail=20
   ```
   Ожидается: все сервисы в статусе `Up`, в логах бота — `bot started`, в логах http-service — `http-service listening on :8080`.

2. **Health HTTP-сервиса**
   ```bash
   curl -s http://localhost:8080/health
   ```
   Ожидается: ответ `200 OK` (тело может быть пустым или `ok`).

3. **Ручная проверка в Telegram**
   - Написать боту `/start` — должно прийти приветствие.
   - `/register ваш_email@example.com` — ответ «Готово! Теперь просто пришли ссылку на файл».
   - Отправить любую ссылку на файл (например, на маленькое изображение) → появится клавиатура «На email / В этот чат / И туда, и туда».
   - Выбрать «В этот чат» — файл должен прийти в чат (или сообщение об ошибке, если URL недоступен).

4. **Сборка и автотесты**
   ```bash
   go build ./...
   go test ./...
   ```
   Тесты: `extractFirstURL` (bot), `MultiDelivery.SendFile` (delivery), обработчик `/health` (httpserver). Файлы тестов — `*_test.go` рядом с кодом в `internal/bot`, `internal/delivery`, `internal/httpserver`.

# Особенности доставки

- При выборе доставки на email бот может предупредить, если у пользователя Gmail и расширение файла относится к блокируемым (например, `.exe`, `.bat`, `.js` и др.) — в этом случае отправка на почту не выполняется, и предлагается использовать доставку в чат.

# Переменные окружения

## Telegram‑бот

| Переменная | Обязательная | Описание |
|-------------|---------------|-----------|
| `TELEGRAM_TOKEN` | да | Токен бота от [@BotFather](https://t.me/BotFather) |
| `ADMIN_CHAT_ID` | да | Telegram ID администратора для подтверждения смены email |
| `API_BASE` | нет | Адрес HTTP‑сервиса (по умолчанию `http://http-service:8080`) |

---

## Локальный Telegram Bot API

| Переменная | Обязательная | Описание |
|-------------|---------------|-----------|
| `TELEGRAM_API_ID` | да | `api_id` из раздела *API Development Tools* на [my.telegram.org](https://my.telegram.org) |
| `TELEGRAM_API_HASH` | да | `api_hash` из раздела *API Development Tools* на [my.telegram.org](https://my.telegram.org) |
| `TELEGRAM_API_BASE` | нет | Адрес локального Bot API внутри Docker‑сети (по умолчанию `http://telegram-bot-api:8081`) |

---

## База данных

| Переменная | Обязательная | Описание |
|-------------|---------------|-----------|
| `DB_DSN` | да | Строка подключения к PostgreSQL, например `postgres://user:pass@postgres:5432/dbname?sslmode=disable` |
| `POSTGRES_USER` | да | Пользователь PostgreSQL |
| `POSTGRES_PASSWORD` | да | Пароль PostgreSQL |
| `POSTGRES_DB` | да | Имя базы данных PostgreSQL |

---

## SMTP (email‑доставка)

| Переменная | Обязательная | Описание |
|-------------|---------------|-----------|
| `SMTP_HOST` | да | Хост SMTP‑сервера, например `smtp.yandex.ru` |
| `SMTP_PORT` | да | Порт SMTP‑сервера, например `25` или `587` |
| `SMTP_USER` | нет | Логин SMTP (если требуется авторизация) |
| `SMTP_PASS` | нет | Пароль SMTP (если требуется авторизация) |
| `SMTP_FROM` | да | Email отправителя, например [`bot@example.com`](mailto:bot@example.com) |

---

# Команды бота

| Команда | Описание |
|----------|-----------|
| `/start` | Приветствие и проверка регистрации |
| `/register email@example.com` | Регистрация и генерация API‑ключа |
| `/change_email new@example.com` | Запрос на смену email (требует подтверждения админа) |
| `/send <ссылка>` | Отправить файл по ссылке (можно просто прислать ссылку без команды) |
| `/help` | Список доступных команд |

---

## Команды администратора

| Команда | Описание |
|----------|-----------|
| `/approve_change <id>` | Подтвердить заявку на смену email |
| `/reject_change <id>` | Отклонить заявку на смену email |
| `/list_changes` | Показать все активные заявки |

Эти команды доступны только в административном чате (`ADMIN_CHAT_ID`).

---

## HTTP API

HTTP‑сервис предоставляет эндпоинт:

- `POST /send` — запрос на доставку файла пользователю.

Тело запроса (JSON):

```json
{
  "api_key": "USER_API_KEY",
  "file_url": "https://example.com/file.zip",
  "mode": "email | telegram | both"
}
```

- `api_key` — обязательный, API‑ключ пользователя из таблицы `users`;
- `file_url` — обязательный, URL файла для скачивания;
- `mode` — необязательный, по умолчанию `email`.

---

# Логи

HTTP‑сервис пишет структурированные логи в `/logs/send.log` (внутри контейнера). На хосте этот путь обычно монтируется в локальный каталог (например, `./http-logs:/logs` в `docker-compose.yml`).

# Примеры логов доставки

## Пример для email‑доставки

```text
user_id=1 username=user url=https://... mode=email status=received
user_id=1 username=user url=https://... mode=email status=downloading
user_id=1 username=user url=https://... mode=email status=downloaded size=1351081
user_id=1 username=user email=user@example.com url=https://... mode=email status=sent size=1351081
```
## Пример для Telegram‑доставки:
```text
telegram delivery: user_id=1 telegram_id=123456 url=https://... mode=telegram status=request
telegram delivery: user_id=1 telegram_id=123456 url=https://... mode=telegram status=sent
```

## Схема работы 

```text
Пользователь в Telegram
    │
    │ 1. /register email@example.com
    ▼
Telegram‑бот (bot)
    │
    │ 1.1. Проверяет, есть ли telegram_id в базе
    │ 1.2. Если нет — создаёт пользователя:
    │      - INSERT INTO users (email, api_key)
    │      - INSERT INTO telegram_users (telegram_id, username, user_id)
    ▼

Пользователь в Telegram
    │
    │ 2. Отправляет ссылку (или /send <url>)
    ▼
Telegram‑бот (bot)
    │
    │ 2.1. Извлекает первую URL из сообщения
    │ 2.2. Проверяет регистрацию (telegram_users)
    │ 2.3. Сохраняет ссылку в pendingLinks[telegram_id]
    │ 2.4. Показывает inline‑клавиатуру:
    │      - На email
    │      - В этот чат
    │      - И туда, и туда
    ▼

Пользователь в Telegram
    │
    │ 3. Нажимает кнопку (email / telegram / both)
    ▼
Telegram‑бот (bot)
    │
    │ 3.1. По telegram_id берёт ссылку из pendingLinks
    │ 3.2. (для email/both) Проверяет:
    │      - email пользователя
    │      - если Gmail + «опасное» расширение (.exe, .bat, .js, ...),
    │        предупреждает и НЕ шлёт запрос в HTTP‑сервис
    │ 3.3. Получает api_key по telegram_id
    │ 3.4. Делает HTTP‑запрос:
    │      POST {API_BASE}/send
    │      Body: {api_key, file_url, mode}
    │ 3.5. Удаляет ссылку из pendingLinks
    ▼

HTTP‑сервис (http-service)
    │
    │ 4. Принимает POST /send
    │ 4.1. Валидирует JSON (api_key, file_url, mode)
    │ 4.2. По api_key находит пользователя:
    │      - JOIN users + telegram_users
    │ 4.3. Определяет режим доставки:
    │      - email      → EmailDelivery
    │      - telegram   → TelegramDelivery
    │      - both       → MultiDelivery (email + telegram)
    ▼

Слой доставки (delivery)
    │
    ├─ EmailDelivery
    │    │
    │    │ 5.1. GET file_url → временный файл
    │    │ 5.2. Формирует письмо с вложением
    │    │ 5.3. Отправляет через SMTP (SMTP_HOST/PORT/USER/PASS/FROM)
    │    │ 5.4. Пишет логи в /logs/send.log
    │
    ├─ TelegramDelivery
    │    │
    │    │ 5.1. GET file_url → временный файл
    │    │ 5.2. POST {TELEGRAM_API_BASE}/bot<TOKEN>/sendDocument
    │    │      - multipart/form-data
    │    │      - chat_id = telegram_id
    │    │      - document = бинарное содержимое файла
    │    │ 5.3. Локальный telegram-bot-api отправляет файл в Telegram
    │    │ 5.4. Пишет логи в /logs/send.log
    │
    └─ MultiDelivery
         │
         │ 5.x. Вызывает EmailDelivery и/или TelegramDelivery
         │      и агрегирует ошибки (если обе упали)
         ▼

Пользователь
    │
    ├─ Получает письмо с вложением (email‑режим)
    └─ Получает файл в Telegram‑чате (telegram‑режим)
```