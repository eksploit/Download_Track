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

cmd/
bot/ — точка входа Telegram‑бота
http-service/ — точка входа HTTP‑сервиса
internal/
bot/ — логика бота (команды, inline‑клавиатура, CallbackQuery)
httpserver/ — HTTP‑сервис (/health, /send)
delivery/
delivery.go — интерфейс Delivery и тип User
email.go — EmailDelivery (SMTP)
telegram.go — TelegramDelivery (локальный Bot API)
multi.go — MultiDelivery (email + Telegram одновременно)

text

## Быстрый старт

1. Скопировать `.env.example` в `.env` и заполнить все параметры.

2. Запустить сервисы:
   ```bash
   docker compose up --build -d
Написать боту в Telegram, выполнить /start, затем /register email@example.com и отправить ссылку на файл.

В ответ бот предложит выбрать способ доставки через inline‑клавиатуру:

На email — файл придёт на зарегистрированный email.

В этот чат — файл придёт прямо в Telegram‑чат.

И туда, и туда — файл будет доставлен обоими способами.

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
| `/help` | Список доступных команд |

---

## Команды администратора

| Команда | Описание |
|----------|-----------|
| `/approve_change <id>` | Подтвердить заявку на смену email |
| `/reject_change <id>` | Отклонить заявку на смену email |
| `/list_changes` | Показать все активные заявки |

---

# Логи

HTTP‑сервис пишет структурированные логи в `/logs/send.log`.

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
Пользователь
    │ ссылка
    ▼
filemailer-bot
    │ POST /send mode=telegram
    ▼
filemailer-http (TelegramDelivery)
    │ GET https://site.com/file.exe
    ▼
Интернет
    │ файл сохраняется в /tmp/tgdl-xxx-file.exe
    ▼
filemailer-http (TelegramDelivery)
    │ POST /bot<TOKEN>/sendDocument
    │ Content-Type: multipart/form-data
    │ поле chat_id = 123
    │ поле document = <бинарное содержимое файла>
    ▼
telegram-bot-api
    │ ✅ принимает файл как бинарные данные
    │ MTProto
    ▼
Серверы Telegram
    │
    ▼
Пользователь получает файл в чат

```