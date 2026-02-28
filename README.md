# Download Track Bot

Телеграм‑бот и HTTP‑сервис на Go для отправки файлов с URL на email пользователя. [web:12][web:63]

## Возможности

- Регистрация пользователя по команде `/register email@example.com`, генерация API‑ключа. [web:12]
- Приём любых сообщений с URL, проксирование ссылки в HTTP‑сервис. [web:22]
- HTTP‑сервис скачивает файл по URL и отправляет его как вложение на зарегистрированный email (SMTP). [web:57]
- Запрос смены email через `/change_email`, подтверждение/отклонение админом в отдельном чате.
- Хранение пользователей и заявок в PostgreSQL.

## Стек

- Go, `github.com/go-telegram-bot-api/telegram-bot-api/v5` для бота. [web:12][web:63]
- PostgreSQL для хранения пользователей и заявок.
- Docker Compose для запуска `bot`, `http-service` и `postgres`.
- SMTP‑сервер для исходящей почты.

## Быстрый старт

1. Скопировать `.env.example` в `.env`, заполнить `TELEGRAM_TOKEN`, `DB_DSN`, SMTP‑параметры.
2. Запустить сервисы:

   ```bash
   docker compose up --build -d
Написать боту в Telegram, выполнить /start, затем /register email@example.com и отправить ссылку на файл.
3. Написать боту в Telegram, выполнить /start, затем /register email@example.com и отправить ссылку на файл.
