# Project Plan: Внедрение фич из беклога

## Overview
Реализация двух ключевых улучшений интерфейса и архитектуры из документации `BACKLOG.md`:
1. Обеспечение приватности списков MAX-каналов за счет перехода на ручной ввод ID во фронтенде (вместо общего кэша).
2. Крупный архитектурный переход подсистемы загрузки истории (MTProto `gotd/td`) с корпоративной модели (один технический аккаунт) на коммерческую модель SaaS (изолированные сессии для каждого клиента).

## Project Type
WEB / BACKEND

## Success Criteria
- [x] В Mini App отсутствует выпадающий список MAX-каналов; используется текстовое поле с подсказкой.
- [x] Бэкенд больше не экспонирует весь список MAX-каналов наружу через `/api/max/chats`.
- [x] Панель управления (Mini App) имеет интерфейс, позволяющий пользователю привязать свой персональный сеанс Telegram для MTProto.
- [x] Сессии MTProto (Session Bytes) хранятся в БД в новой таблице `user_mtproto_sessions` (или аналогичной схеме).
- [x] Обработчик `sync_worker.go` динамически поднимает MTProto-клиент только во время задачи, используя сохраненную сессию заказчика.

## Tech Stack
- **Frontend**: HTML/CSS/JS (Vanilla) - для изменения UI и добавления флоу авторизации.
- **Backend**: Golang (PostSync, gotd/td) - для адаптации sync-воркера.
- **Database**: PostgreSQL & SQLite - для миграций и хранения сессий.

## File Structure
- `frontend/index.html` *(изменения UI)*
- `api.go` *(удаление `/api/max/chats`, добавление эндпоинтов для начала авторизации)*
- `sync_worker.go` *(переработка глобального процесса в изолированный/динамический)*
- `repository.go`, `postgres.go`, `sqlite.go` *(новые методы БД)*
- БД миграции `migrations/` *(определение новой таблицы для сессий)*

## Task Breakdown

### Phase 1: Изоляция MAX-каналов (Ручной ввод ID)
### Task 1.1: Frontend UI Обновление
- **Description:** Заменить интерфейс создания связки — убрать `select` и добавить `input type="number"` для MAX-канала. Убрать вызов API загрузки `chats`.
- **Agent:** `frontend-specialist`
- **Skill:** `frontend-design`
- **INPUT:** `frontend/index.html`
- **OUTPUT:** Готовая форма, которая сразу использует `document.getElementById('pair-max-id').value`.
- **VERIFY:** UI отображается корректно, нет ошибки 404/ошибок скрипта при загрузке. Связки создаются успешно.

### Task 1.2: Backend API Отключение
- **Description:** Удалить или отключить обработчик `/api/max/chats` из бэкенда, так как он больше не нужен фронтенду.
- **Agent:** `backend-specialist`
- **Skill:** `clean-code`
- **INPUT:** `api.go`
- **OUTPUT:** Лишний эндпоинт вычищен из роутера.
- **VERIFY:** Приложение успешно компилируется, роут `/api/max/chats` возвращает `404 Not Found`.

---

### Phase 2: Мультиарендная (SaaS) архитектура MTProto
### Task 2.1: База данных (Хранение сессий)
- **Description:** Добавить таблицу `user_mtproto_sessions` (`user_id`, `session_data` в виде BLOB/BYTEA). Добавить CRUD-операции в `Repository`.
- **Agent:** `database-architect`
- **Skill:** `database-design`
- **INPUT:** `migrations/`, `sqlite.go`, `postgres.go`, `repository.go`
- **OUTPUT:** Миграции БД применены, методы `GetMTProtoSession` и `SaveMTProtoSession` (с поддержкой `gotd/session` Storage) готовы к использованию.
- **VERIFY:** SQL-запросы корректны для обоих движков, тесты репозитория проходят успешно.

### Task 2.2: API авторизации MTProto
- **Description:** Создать REST API (или WebSocket) для прохождения шагов авторизации Telegram (запрос телефона -> запрос кода -> запрос пароля 2FA) напрямую из устройства пользователя.
- **Agent:** `backend-specialist`
- **Skill:** `api-patterns`
- **INPUT:** `api.go`
- **OUTPUT:** Новая группа эндпоинтов в `b.mux`, позволяющая инициировать вход клиента в MTProto.
- **VERIFY:** `gotd/td` flow авторизации возвращает сессию, которая сохраняется в БД по ID клиента.

### Task 2.3: Frontend UI для авторизации Telegram (SaaS)
- **Description:** Добавить в Mini App блок "Подключить Telegram аккаунт" (для возможности скачивать историю). Формы ввода номера, кода из SMS и облачного пароля.
- **Agent:** `frontend-specialist`
- **Skill:** `frontend-design`
- **INPUT:** `frontend/index.html`
- **OUTPUT:** Модальные окна или секции для пошагового ввода данных.
- **VERIFY:** Пользователь может ввести номер, получить код и залогиниться. Интерфейс отзывчив.

### Task 2.4: Изолированный `sync_worker`
- **Description:** Полностью рефакторить `sync_worker.go`. Отказаться от глобального клиента `client.Run(ctx, ...)`. Теперь `processSyncTask` должен инициализировать `telegram.NewClient` с сессией из базы для конкретного пользователя, работать с историей и закрываться `client.Close()`.
- **Agent:** `backend-specialist`
- **Skill:** `clean-code`, `parallel-agents`
- **INPUT:** `sync_worker.go`
- **OUTPUT:** Модуль без привязки к `TG_PHONE` и глобальной сессии. Каждый клиентский запрос скачивает историю самостоятельно.
- **VERIFY:** Синхронизация проходит успешно от лица нужного подключенного пользователя. Утечек горутин/соединений не возникает.

## Phase X: Verification
- [x] **Lint**: `go vet ./...` & `golangci-lint run` (код чист)
- [x] **Tests**: `go test -v ./...` (репозиторий и API работают)
- [x] **Build**: Бэкенд `go build` собирается без проблем
- [x] **Manual UX Check**: Пользовательские данные не смешиваются, сессии полностью изолированы
- [x] **Socratic Gate**: Проверен перед выполнением
