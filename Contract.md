# Contract.md — Единый контракт взаимодействия Frontend ↔ Backend

Этот документ фиксирует **строгие сигнатуры всех HTTP REST-эндпоинтов, структур JSON и WebSocket-сообщений**. Оба слоя (`backend/` на Go и `frontend/` на TypeScript) обязаны реализовывать данный контракт буква в букву.

---

## 1. Базовые конвенции протокола

### 1.1. Транспорт, Префикс и Форматы
- **Базовый префикс REST API**: `/api/v1`
- **WebSocket Endpoint**: `/ws` (авторизация по `httpOnly` cookie при HTTP Upgrade)
- **Раздача публичных медиафайлов**: `/storage/{s3_key}`
- **Формат JSON**: `snake_case` для всех ключей во всех запросах, ответах и WebSocket-сообщениях.
- **Даты и время**: всегда строка в формате RFC3339 / ISO-8601 в UTC: `"2026-11-12T14:30:00Z"`.

### 1.2. Аутентификация, Cookies и Спец-заголовки
- Токены передаются исключительно в `httpOnly` куках (`SameSite=Lax`, `Path=/`):
  - `ege_access` — JWT (TTL 15 мин). Payload: `{ "sub": "<user_uuid>", "role": "student|teacher|superuser", "exp": ... }`
  - `ege_refresh` — криптостойкий токен или JWT (TTL 30 дней, хранится/валидируется через Redis для мгновенного отзыва).
- Если запрос возвращает `401 Unauthorized` с кодом `"TOKEN_EXPIRED"`, клиент автоматически вызывает `POST /api/v1/auth/refresh` и повторяет исходный запрос.
- **Заголовок Impersonation (только для `superuser`)**:
  - `X-Impersonate-User: <target_user_uuid>` — если заголовок передан валидным `superuser`, бэкенд выполняет все бизнес-запросы от лица указанного пользователя (в ответах `/auth/me` поле `impersonated_by` содержит реальный UUID суперюзера). Если этот заголовок шлёт не `superuser` → `403 Forbidden`.

### 1.3. Стандартная обёртка ошибок и пагинации
Любая ошибка (`4xx`, `5xx`) возвращает единый JSON:
```json
{
  "error": {
    "code": "SUBSCRIPTION_READ_ONLY",
    "message": "Срок действия подписки истёк. Группа доступна только для чтения.",
    "details": {}
  }
}
```
Основные коды `error.code`:
- `UNAUTHORIZED`, `TOKEN_EXPIRED`, `FORBIDDEN`, `ONBOARDING_REQUIRED`
- `VALIDATION_ERROR`, `NOT_FOUND`, `CONFLICT`
- `SUBSCRIPTION_REQUIRED`, `SUBSCRIPTION_READ_ONLY`, `LIMIT_EXCEEDED`
- `ATTEMPTS_EXCEEDED`, `DEADLINE_PASSED`, `SESSION_FINISHED`, `RATE_LIMITED`

Стандартный ответ списков с пагинацией:
```json
{
  "items": [],
  "total": 142,
  "limit": 20,
  "offset": 0
}
```

---

## 2. Ключевые доменные JSON-схемы (Ядро КЕГЭ)

### 2.1. Пользователь и Подписка (`UserDTO`)
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "username": "ivan_inf",
  "first_name": "Иван",
  "last_name": "Петров",
  "role": "teacher",
  "is_onboarded": true,
  "linked_providers": ["yandex", "vk"],
  "subscription": {
    "is_active": true,
    "is_read_only": false,
    "tier": {
      "id": 2,
      "slug": "pro_teacher",
      "name": "Учитель ПРО",
      "price_rub": 1490,
      "features": {
        "max_rooms": 15,
        "max_students_per_room": 40,
        "custom_tasks_enabled": true,
        "proctoring_enabled": true,
        "live_code_watch_enabled": true,
        "mcp_enabled": true
      }
    },
    "starts_at": "2026-11-01T00:00:00Z",
    "expires_at": "2026-12-01T00:00:00Z"
  },
  "impersonated_by": null,
  "created_at": "2026-09-01T10:00:00Z"
}
```
> Примечание: если `first_name == ""` или `username` сгенерирован временный после первого OAuth-входа (`is_onboarded == false`), фронтенд перенаправляет пользователя на модалку/экран завершения регистрации (`PATCH /api/v1/users/me/onboarding`).

### 2.2. Полиморфная схема конфигурации ответа (`answer_config`), правильного ответа (`correct_answer`) и ответа ученика (`user_answer`)

Поддерживаются **4 строгих типа `answer_type`**, покрывающих все задания КЕГЭ №1–27:

#### Тип A: `"single"` — Одиночная строка или число (Задания №1–16, 22, 23, 24)
- `answer_config`:
```json
{
  "answer_type": "single",
  "max_score": 1,
  "case_sensitive": false,
  "trim_whitespace": true,
  "placeholder": "Введите ответ"
}
```
- `correct_answer` (массив допустимых эквивалентных написаний, например `"12,5"` и `"12.5"`):
```json
{
  "answer_type": "single",
  "accepted": ["42"]
}
```
- `user_answer`:
```json
{
  "answer_type": "single",
  "value": "42"
}
```

#### Тип B: `"pair"` — Два поля в одной строке (Задания №17, 18, 26, а также №27 Файл А + Файл Б)
- `answer_config`:
```json
{
  "answer_type": "pair",
  "max_score": 2,
  "labels": ["Файл А", "Файл Б"],
  "scoring_rule": "per_cell_or_exact"
}
```
  *Правила `scoring_rule` для `"pair"`*:
  - `"exact_only"` (`max_score: 1`, например №17, №18): 1 балл только если совпали оба числа.
  - `"per_item"` (`max_score: 2`, например №26, №27): по 1 баллу за каждое верно угаданное поле в своей позиции (либо 1 балл если числа перепутаны местами при флаге `"allow_swap_for_1pt": true`).
- `correct_answer`:
```json
{
  "answer_type": "pair",
  "values": ["124", "984512"],
  "allow_swap_for_1pt": false
}
```
- `user_answer`:
```json
{
  "answer_type": "pair",
  "values": ["124", "984500"]
}
```

#### Тип C: `"table"` — Двумерная таблица ответов (Задание №25 и нестандартные табличные задачи)
- `answer_config`:
```json
{
  "answer_type": "table",
  "rows": 6,
  "cols": 2,
  "col_headers": ["Число", "Делитель"],
  "max_score": 2,
  "ignore_trailing_empty_rows": true,
  "partial_1pt_max_errors": 1
}
```
  *Проверка*: пустые хвостовые строки игнорируются (как на реальной станции КЕГЭ, где дают таблицу с запасом в 10 строк, а в ответе только 5 пар). Если все непустые строки совпали → `max_score`. Если `partial_1pt_max_errors > 0` и допущено не более N ошибочных/пропущенных строк → `1` балл.
- `correct_answer`:
```json
{
  "answer_type": "table",
  "matrix": [
    ["800001", "23"],
    ["800015", "47"]
  ]
}
```
- `user_answer`:
```json
{
  "answer_type": "table",
  "matrix": [
    ["800001", "23"],
    ["800015", "47"],
    ["", ""]
  ]
}
```

#### Тип D: `"triplet_19_21"` — Единая карточка заданий №19, №20, №21
У задачи `kim_numbers = [19, 20, 21]`, `max_score = 3` (по 1 первичному баллу за каждый подпункт!).
- `answer_config`:
```json
{
  "answer_type": "triplet_19_21",
  "max_score": 3,
  "items": [
    { "kim": 19, "kind": "single", "label": "Задание 19" },
    { "kim": 20, "kind": "pair",   "label": "Задание 20 (два значения по возрастанию)" },
    { "kim": 21, "kind": "single", "label": "Задание 21" }
  ]
}
```
- `correct_answer`:
```json
{
  "answer_type": "triplet_19_21",
  "ans_19": ["18"],
  "ans_20": ["9", "17"],
  "ans_21": ["16"]
}
```
- `user_answer`:
```json
{
  "answer_type": "triplet_19_21",
  "ans_19": "18",
  "ans_20": ["9", "17"],
  "ans_21": "14"
}
```

### 2.3. Схема карточки задачи (`TaskDTO`)
```json
{
  "id": 1042,
  "owner_id": null,
  "is_public_bank": true,
  "kim_numbers": [24],
  "topic": {
    "id": 2401,
    "kim_number": 24,
    "name": "Обработка символьных строк: метод двух указателей"
  },
  "difficulty": 3,
  "source": "Основная волна 2025",
  "text_md": "Текстовый файл **24.txt** состоит из символов `A`, `B`, `C`...",
  "images": [
    "/storage/tasks/images/8f12c9a.webp"
  ],
  "files": [
    {
      "id": "f1",
      "name": "24.txt",
      "url": "/storage/tasks/files/1042/24.txt",
      "size_bytes": 6291456,
      "ext": "txt"
    }
  ],
  "answer_config": {
    "answer_type": "single",
    "max_score": 1,
    "case_sensitive": false,
    "trim_whitespace": true
  },
  "stats": {
    "solve_rate": 38.5,
    "avg_attempts": 2.4,
    "avg_solve_time_sec": 540,
    "attempts_histogram": [120, 85, 40, 12, 5, 2, 0, 0, 0, 0, 0, 0, 0, 0, 1],
    "bank_solvers_count": 412
  },
  "user_state": {
    "is_attempted": true,
    "is_correct": false,
    "earned_score": 0,
    "total_attempts": 3,
    "last_user_answer": { "answer_type": "single", "value": "1023" },
    "can_view_solution": true
  },
  "solution": {
    "correct_answer": { "answer_type": "single", "accepted": ["1024"] },
    "solution_md": "Заметим, что оптимальная подстрока...",
    "solution_code": "with open('24.txt') as f:\n    s = f.readline().strip()\n..."
  },
  "created_at": "2026-10-01T12:00:00Z"
}
```
> **Строгие правила безопасности `TaskDTO` на бэкенде**:
> 1. Поле `solution` равно `null` для гостей, а также для авторизованных пользователей, если в Банке `is_correct == false && total_attempts < 3`, либо в работе учителя согласно `reveal_policy`.
> 2. Поле `source` равно `null`, если задача запрашивается внутри активной сессии работы (`assignment_session.status == "in_progress"`).

---

## 3. REST API Эндпоинты (`/api/v1`)

### 3.1. Auth & Профиль (`/auth`, `/users`)
| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/auth/oauth/{provider}/login` | Public | `provider` ∈ `yandex`, `vk`. Опциональный `?redirect_to=/groups/join/ABC`. Генерирует `state` в куке и делает `302 Redirect` на OAuth-провайдера. |
| `GET` | `/auth/oauth/{provider}/callback` | Public | Принимает `code` & `state`, создаёт/находит юзера, ставит `httpOnly` куки `ege_access` + `ege_refresh`, делает `302 Redirect` на фронтенд. |
| `POST` | `/auth/refresh` | Cookie `ege_refresh` | Ротация пары токенов в куках. Возвращает `200 { "ok": true }`. |
| `POST` | `/auth/logout` | Auth | Инвалидирует refresh-токен в Redis, очищает куки. |
| `GET` | `/auth/me` | Public / Auth | Возвращает `{ "authenticated": true, "user": UserDTO }` или `{ "authenticated": false, "user": null }` (удобно вызывать на старте SPA без красного 401 в консоли). |
| `PATCH` | `/users/me/onboarding` | Auth | Тело: `{ "username": "...", "first_name": "...", "last_name": "..." }`. Валидирует уникальность `username` и заполняет ФИ. |
| `PATCH` | `/users/me` | Auth | Обновление `username`, `first_name`, `last_name`. |

### 3.2. Подписки и Инвайты (`/subscriptions`)
| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/subscriptions/tiers` | Public | Список публичных тарифов (`is_public = true`) для страницы описания учительских возможностей. |
| `GET` | `/subscriptions/invites/{code}` | Public | Предпросмотр инвайт-ссылки: название тарифа, срок в днях, валидна ли ещё ссылка. |
| `POST` | `/subscriptions/invites/{code}/claim` | Auth | Активирует инвайт для текущего юзера: повышает `role` до `teacher` (если был `student`), создаёт/продлевает `user_subscriptions`. |

### 3.3. Банк задач, Справочники и Главная ученика (`/topics`, `/bank`, `/dashboard`)
| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/topics` | Public | Возвращает все темы, сгруппированные по `kim_number` (1..27). Кэшируется в Redis. |
| `GET` | `/bank/tasks` | Public / Auth | Query: `q` (текст или `#id`), `kim` (`1..27`), `topic_id`, `difficulty` (`1..4`), `status` (`all\|solved\|unsolved\|attempted`), `sort` (`newest\|oldest\|solve_rate_asc\|solve_rate_desc`), `limit`, `offset`. **Важно**: для авторизованного юзера исключает задачи его текущих активных `mock_exam` и `proctored_test`! |
| `GET` | `/bank/tasks/{id}` | Public / Auth | Карточка одной задачи из банка (также возвращает `403 TASK_LOCKED_BY_ACTIVE_EXAM`, если эта задача прямо сейчас в активном пробнике юзера). |
| `POST` | `/bank/tasks/{id}/submit` | Auth | Тело: `{ "user_answer": { ... }, "recommendation_log_id": 123 }`. Проверяет ответ, инкрементирует попытки, пересчитывает `user_topic_stats`, возвращает `{ "is_correct": bool, "earned_score": int, "max_score": int, "total_attempts": int, "can_view_solution": bool, "solution": ... }`. |
| `GET` | `/dashboard/student` | Auth | Данные для Главной страницы (`/`): 1) активные работы/дедлайны из групп юзера; 2) матрица прогресса по номерам КИМ 1..27 (`solved`, `attempted`, `success_rate`); 3) список слабых тем (`score DESC`); 4) блок рекомендованных задач (`weak_topic` + `adaptive_difficulty`) с записью в `task_recommendation_logs`. *(Без прогноза балла!).* |

### 3.4. Группы / Комнаты (`/rooms`)
| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/rooms` | Auth | Список групп: если `view_mode=teacher` — созданные учителем комнаты (с флагом `is_read_only` при истёкшей подписке); если `student` — комнаты, где юзер состоит как ученик. |
| `POST` | `/rooms` | Teacher (Active Sub) | Создание комнаты: `{ "name": "11-А Информатика", "max_members": 30 }`. Проверяет лимит `max_rooms` тарифа. Генерирует короткий `invite_code` (8 символов, без похожих `0/O/I/l`). |
| `GET` | `/rooms/{room_id}` | Room Member / Creator | Детали комнаты, участники (ФИ + username + онлайн-статус), список работ (`assignments`). |
| `PATCH` | `/rooms/{room_id}` | Creator (Active Sub) | Переименование, смена `max_members`, перевыпуск `invite_code`. |
| `DELETE` | `/rooms/{room_id}` | Creator | Soft-delete комнаты (`deleted_at = now()`). |
| `GET` | `/rooms/join/{invite_code}` | Public / Auth | Информация о комнате перед вступлением (название, ФИ учителя). |
| `POST` | `/rooms/join/{invite_code}` | Auth | Вступление текущего юзера в комнату (проверяет `max_members` комнаты и тарифа учителя). |
| `DELETE` | `/rooms/{room_id}/members/{user_id}` | Creator / Self | Исключение ученика учителем или выход ученика из группы. |
| `PUT` | `/rooms/{room_id}/free-code` | Room Member | Сохранение кода из свободного редактора комнаты в приватный S3. |

### 3.5. Работы учителя, Конструктор и Самостоятельные пробники (`/assignments`, `/sessions`)
Режимы `mode`: `"homework"` | `"classwork"` | `"practice"` | `"proctored_test"` | `"mock_exam"`.

Структура `AssignmentSettings`:
```json
{
  "max_attempts": 4,
  "duration_minutes": 235,
  "starts_at": "2026-11-15T07:00:00Z",
  "deadline_at": "2026-11-20T20:59:59Z",
  "allow_after_deadline": false,
  "reveal_policy": "after_submit",
  "show_secondary_score": true,
  "proctoring_enabled": true,
  "tools_allowed": {
    "ide": true,
    "spreadsheet": true,
    "drawing": true
  }
}
```
*Значения `reveal_policy`*:
- `"immediate"` — сразу после каждой попытки показывать верно/неверно (идеально для практики/ДЗ).
- `"after_submit"` — показывать результаты сразу после нажатия «Завершить работу» учеником.
- `"after_deadline"` — показывать результаты только после наступления `deadline_at`.
- `"manual"` — результаты скрыты, пока учитель не переключит флаг `results_released = true`.

| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `POST` | `/rooms/{room_id}/assignments` | Creator (Active Sub) | Создание работы в группе: `{ "title": "...", "mode": "...", "settings": {...}, "task_ids": [101, 102, ...] }`. |
| `PATCH` | `/assignments/{id}` | Creator (Active Sub) | Изменение настроек, дедлайна, публикация результатов (`results_released`). |
| `DELETE` | `/assignments/{id}` | Creator | Soft-delete работы. |
| `POST` | `/exams/self-start` | Auth (Student) | Запуск **самостоятельного пробника КЕГЭ** вне группы (`room_id = null`, `mode = "mock_exam"`, `duration_minutes = 235`): либо по готовому пресету варианта, либо автогенерация полного варианта из нерешённых задач по номерам КИМ 1..27 (где связка 19–21 занимает позиции 19–21 одной карточкой = итого 25 карточек на 27 номеров КИМ и 29 первичных баллов!). Возвращает `session_id`. |
| `POST` | `/assignments/{id}/start` | Member | Создаёт или возвращает текущую `assignment_session` ученика (фиксирует `started_at` для персонального таймера). |
| `GET` | `/sessions/{session_id}` | Owner / Room Teacher | Полное состояние сессии: таймер (`server_now`, `started_at`, `expires_at`), упорядоченный массив задач (без `source` и `solution` во время сдачи!), сохранённые ответы ученика. |
| `POST` | `/sessions/{session_id}/tasks/{task_id}/answer` | Session Owner | Сохранение/отправка ответа на задачу внутри сессии + опционально код `{ "user_answer": {...}, "code": "...", "paste_ranges": [[10, 45]], "student_comment": "..." }`. Записывает код в приватный S3, проверяет лимит попыток, пушит дельту учителю по WebSocket. |
| `POST` | `/sessions/{session_id}/finish` | Session Owner | Досрочное или таймерное завершение сессии: финальный подсчёт `primary_score` (0..29) и `secondary_score` (0..100 по таблице `exam_score_scale`), возврат итогового протокола (если позволяет `reveal_policy`). |

### 3.6. Аналитика и Live-дашборд учителя (`/teacher`)
*(Доступно создателю группы даже в Read-Only режиме после окончания подписки!)*
| Метод | Путь | Доступ | Описание |
|---|---|---|---|
| `GET` | `/assignments/{id}/matrix` | Room Creator | Начальный снэпшот для **Live-матрицы класса**: список всех учеников группы, статус сессии, онлайн-индикатор, текущая открытая задача, счётчик уходов с вкладки и вставок кода, баллы и попытки по каждой задаче + агрегаты по столбцам (`assignment_task_stats`). Далее обновляется в реальном времени по WebSocket! |
| `GET` | `/sessions/{session_id}/inspect` | Room Creator | Детальное досье на выполнение работы конкретным учеником: полный хронологический массив событий `user_task_events` с `payload` для построения **интерактивного Таймлайна**, сохранённые коды из S3 и `paste_ranges` по каждой задаче. |
| `POST` | `/teacher/tasks` | Teacher (Active Sub) | Создание приватной авторской задачи учителя (`owner_id = current_user.id`). |
| `GET` | `/teacher/tasks` | Teacher | Список собственных задач учителя для добавления в свои работы. |

### 3.7. Админка и Контент-студия (`/admin`)
*(Строго `role == "superuser"`, кроме редактора публичного банка при наличии прав).*
| Метод | Путь | Описание |
|---|---|---|
| `POST / PATCH / DELETE` | `/admin/tasks`, `/admin/tasks/{id}` | Создание, редактирование и soft-delete задач общего банка. |
| `POST` | `/admin/media/upload` | Multipart-загрузка картинки или файла задачи (`tasks/images/...` или `tasks/files/...`), возвращает `{ "s3_key", "url", "size_bytes", "ext" }`. |
| `POST` | `/admin/tasks/import` | Массовый импорт задач из `.csv` или `.zip` (`tasks.csv` + папка `media/`). Поддерживает режим `?dry_run=true` (валидация и предпросмотр без коммита в БД). |
| `POST / PATCH / DELETE` | `/admin/topics`, `/admin/topics/{id}` | Управление справочником тем внутри номеров КИМ 1..27. |
| `GET / PUT` | `/admin/exam-scale` | Чтение и редактирование таблицы перевода первичных баллов КЕГЭ (0..29) в тестовые (0..100). |
| `GET / POST / PATCH` | `/admin/subscriptions/tiers` | Конструктор тарифов подписок (цены, лимиты, JSON-фичи). |
| `GET / POST / DELETE` | `/admin/subscriptions/invites` | Генерация и отзыв инвайт-ссылок (`/activate/{code}`). |
| `GET / PATCH` | `/admin/users`, `/admin/users/{id}` | Поиск пользователей, ручная выдача роли / подписки на N дней. |

---

## 4. Двухуровневый протокол WebSocket (`/ws`)

Соединение устанавливается один раз авторизованным клиентом по адресу `wss://<domain>/ws`. Формат всех кадров — текстовый JSON с единой шапкой:
```json
{
  "type": "event_name",
  "ts": "2026-11-12T14:35:12.410Z",
  "data": {}
}
```
При обрыве связи клиент переподключается с экспоненциальным backoff (`500ms → 1s → 2s → max 5s`) и заново подписывается на активные каналы. Сервер и клиент обмениваются `ping`/`pong` каждые `25s`.

### 4.1. Уровень 1 — Лёгкая телеметрия сессии и Live-матрица класса (Always-On)

#### Сообщения от Ученика → Серверу (во время прохождения работы `session_id`):
1. `student.session_join` — при открытии экрана теста/пробника:
   ```json
   { "type": "student.session_join", "data": { "session_id": "uuid", "current_task_id": 1042 } }
   ```
2. `student.telemetry_batch` — отправляется раз в `1.5s` (или немедленно при важных событиях `tab_blur`, `insert_in_ide`, `devtools_triggered`):
   ```json
   {
     "type": "student.telemetry_batch",
     "data": {
       "session_id": "uuid",
       "events": [
         {
           "event": "task_opened",
           "task_id": 1042,
           "client_ts": "2026-11-12T14:35:10.100Z",
           "payload": {}
         },
         {
           "event": "insert_in_ide",
           "task_id": 1042,
           "client_ts": "2026-11-12T14:35:11.800Z",
           "payload": {
             "chars_count": 148,
             "preview": "from itertools import product\nfor x in product('0123', repeat=5):",
             "paste_ranges": [[0, 65]]
           }
         },
         {
           "event": "tab_blur",
           "task_id": 1042,
           "client_ts": "2026-11-12T14:35:12.000Z",
           "payload": {}
         }
       ]
     }
   }
   ```
   **Полный enum `event` в телеметрии**:
   - `task_opened`
   - `open_ide` (`payload: { "open": true|false }`)
   - `open_spreadsheet` (`payload: { "file_name": "9.xlsx" }`)
   - `code_run` (`payload: { "duration_ms": 120, "has_error": false }`)
   - `insert_in_ide` (`payload: { "chars_count": int, "preview": string(<=300), "paste_ranges": [[start, end], ...] }`)
   - `tab_blur`
   - `tab_focus` (`payload: { "away_ms": 14200 }`)
   - `answer_submitted` *(также дублируется сервером при REST submit)*
   - `devtools_triggered`

#### Сообщения от Учителя → Серверу (на экране Live-таблицы работы):
1. `teacher.watch_assignment` — подписка на все обновления матрицы конкретной работы:
   ```json
   { "type": "teacher.watch_assignment", "data": { "assignment_id": "uuid" } }
   ```
2. `teacher.unwatch_assignment` — отписка при уходе со страницы.

#### Сообщения от Сервера → Учителю (обновление матрицы и таймлайнов без перезагрузки):
1. `matrix.presence_patch` — мгновенное изменение кружочка статуса ученика:
   ```json
   {
     "type": "matrix.presence_patch",
     "data": {
       "assignment_id": "uuid",
       "user_id": "uuid",
       "session_id": "uuid",
       "online": true,
       "tab_focused": false,
       "current_task_id": 1042,
       "ide_open": true,
       "blur_count": 3,
       "paste_count": 1,
       "last_activity_at": "2026-11-12T14:35:12Z"
     }
   }
   ```
2. `matrix.cell_patch` — ученик отправил ответ или изменил балл по задаче + обновлённая шапка статистики задачи:
   ```json
   {
     "type": "matrix.cell_patch",
     "data": {
       "assignment_id": "uuid",
       "user_id": "uuid",
       "session_id": "uuid",
       "task_id": 1042,
       "is_correct": true,
       "earned_score": 2,
       "attempts": 1,
       "total_primary_score": 14,
       "total_secondary_score": 68,
       "task_stat": {
         "solved_count": 12,
         "attempted_count": 18,
         "avg_attempts": 1.6,
         "avg_time_sec": 310
       }
     }
   }
   ```
3. `matrix.timeline_event` — новое событие для live-доращивания полоски таймлайна в реальном времени.

---

### 4.2. Уровень 2 — Потоковая Live-трансляция кода ученика по запросу (On-Demand Stream)

Чтобы не гонять мегабайты кода от 40 учеников впустую, стриминг кода включается **строго тогда, когда хотя бы один учитель открыл детальное окно наблюдения за конкретной сессией ученика**.

1. **Учитель открывает карточку ученика** и шлёт серверу:
   ```json
   { "type": "teacher.watch_student_live", "data": { "session_id": "uuid" } }
   ```
2. **Сервер проверяет права учителя** (создатель комнаты + тариф `live_code_watch_enabled`), регистрирует подписчика в Hub и, если это первый наблюдатель за `session_id`, отправляет в сокет **Ученика** команду:
   ```json
   { "type": "server.live_stream_toggle", "data": { "session_id": "uuid", "enabled": true, "debounce_ms": 350 } }
   ```
3. **Клиент Ученика**, получив `enabled: true`:
   - Немедленно отправляет полный текущий снимок редактора и консоли (`student.live_code_frame`).
   - Далее при любом изменении текста в Monaco, переключении задачи или выводе в консоль отправляет `student.live_code_frame` с дребезгом (**debounce `350ms`**):
   ```json
   {
     "type": "student.live_code_frame",
     "data": {
       "session_id": "uuid",
       "task_id": 1042,
       "ide_open": true,
       "spreadsheet_open": false,
       "code": "with open('24.txt') as f:\n    s = f.read()\nprint(len(s))",
       "cursor": { "line": 3, "column": 14 },
       "paste_ranges": [[0, 26]],
       "terminal_tail": "6291456\n"
     }
   }
   ```
4. **Сервер ретранслирует** этот кадр `teacher.student_live_frame` только учителям, подписанным на `session_id`, а также кэширует последний кадр в памяти/Redis на случай мгновенного переоткрытия.
5. **Учитель закрывает карточку ученика** (`teacher.unwatch_student_live` или закрытие вкладки): когда счётчик наблюдателей `session_id` падает до `0`, сервер отправляет ученику:
   ```json
   { "type": "server.live_stream_toggle", "data": { "session_id": "uuid", "enabled": false } }
   ```
   И клиент ученика мгновенно прекращает отправку кадров кода!

---

## 5. Формат CSV / ZIP для массового импорта задач (`/admin/tasks/import`)

Администратор может загрузить либо одиночный `tasks.csv` (UTF-8, разделитель `,`, экранирование `"`), либо архив `import.zip`, содержащий `tasks.csv` в корне и папку `media/` с картинками и файлами задач.

**Колонки `tasks.csv`**:
1. `kim_numbers` — номер КИМ (`15`) или связка через точку с запятой (`19;20;21`).
2. `topic_name` — название темы (если темы ещё нет для данного КИМ — создаётся автоматически при коммите).
3. `difficulty` — число `1..4` (`1` Базовый, `2` Средний, `3` Сложный, `4` Гроб).
4. `source` — строка источника (может быть пустой).
5. `text_md` — условие задачи в Markdown + LaTeX (`$...$`, `$$...$$`). Внутри текста ссылки на локальные картинки из архива вида `![](media/graph_1.png)` импортёр автоматически загружает в MinIO и переписывает на `/storage/tasks/images/<uuid>.png`!
6. `images` — дополнительные картинки через `;` (`media/pic1.png;https://...`).
7. `files` — прикреплённые файлы через `;` (`media/24.txt` или `media/27_A.txt;media/27_B.txt`).
8. `answer_type` — `single` | `pair` | `table` | `triplet_19_21`.
9. `answer_config_json` — опциональный JSON тонких настроек таблицы/пары (если пусто — подставляется стандартный конфиг по `answer_type`).
10. `correct_answer_json` — JSON правильного ответа согласно разделу `2.2` (для `single` допускается просто строка, например `42`, которая автоматически обернётся в `{"answer_type":"single","accepted":["42"]}`).
11. `solution_md` — текст разбора (опционально).
12. `solution_code` — код решения на Python (опционально).

