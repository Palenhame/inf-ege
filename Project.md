# Project.md — Архитектура, Продукт, Инфраструктура и Деплой (КЕГЭ Информатика)

Данный документ является верхнеуровневым техническим заданием и архитектурным стандартом платформы подготовки к ЕГЭ по информатике (аналог `codege.ru` нового поколения с live-прокторингом, встроенными в браузер Python IDE + Табличным процессором + Рисовалкой и глубокой аналитикой).

Для параллельной разработки субагентами документация разделена на 4 связанных файла:
1. **`Project.md` (этот файл)** — концепция, роли, бизнес-правила, инфраструктура, Docker, Nginx, деплой, S3, безопасность и порядок параллельной сборки.
2. **`Contract.md`** — строгий контракт взаимодействия Frontend ↔ Backend: все HTTP REST эндпоинты, коды ошибок, JSON-схемы, структуры `answer_config`/`correct_answer`, формат CSV/ZIP-импорта и полный протокол двухуровневых WebSockets.
3. **`Backend.md`** — полное ТЗ на Go-бэкенд и PostgreSQL 17 (полная SQL-схема v2 без эмбеддингов и `subject`, индексы, партиции, архитектура пакетов Go, `pgx/v5`, Redis, MinIO, фоновые воркеры и задел под MCP).
4. **`Frontend.md`** — полное ТЗ на SPA-фронтенд (Vite 8, React 19, TanStack Router, TanStack Query, Zustand, Pyodide 0.27 + OPFS-кэш файлов, Univer Excel-модалка, Live-матрица учителя и интерактивные таймлайны).

---

## 1. Продуктовая концепция и ключевые бизнес-правила

### 1.1. Предметная область
- Платформа заточена **исключительно под ЕГЭ по Информатике (КЕГЭ, задания №1–27)**. Мультипредметности нет (таблица `subject` удалена).
- Поддерживаются все специфичные механики КЕГЭ:
  - Одиночные строковые/числовые ответы (№1–16, 19–24).
  - Парные и табличные ответы произвольной размерности `rows × cols` (№17, 18, 25, 26, 27А/27Б).
  - **Единая карточка связки №19–20–21**: хранится в БД как одна сущность (`kim_numbers = '{19,20,21}'`) с общим условием и тремя независимыми полями ответа (до 3 первичных баллов).
  - **Частичное оценивание** (0 / 1 / 2 / 3 первичных балла) и перевод первичных баллов (0..29) во вторичные тестовые баллы (0..100) для полноформатных пробников (235 минут = 3 ч 55 мин).
  - Прикреплённые к задачам файлы данных (`.txt`, `.csv`, `.xlsx`, `.ods`) и несколько иллюстраций.

### 1.2. Доступность контента и Гостевой режим
- **Неавторизованный гость**:
  - Имеет полный доступ к страницам `/bank` (Банк задач), `/about`, `/terms`, `/login`.
  - В Банке задач может фильтровать, искать задачи, читать условия, скачивать файлы, пользоваться встроенной Python IDE, Табличным процессором и Рисовалкой (код и таблицы хранятся локально в браузере).
  - **НЕ может** отправить ответ на проверку, смотреть правильные ответы/разборы, открывать Главную со статистикой (`/`), Группы (`/groups`) или запускать Пробники — при попытке редирект или промпт на `/login`.
- **Авторизованный ученик (`student`)**:
  - Все функции ученика **полностью бесплатны** без ограничений: проверка задач в банке, персональные рекомендации, статистика по темам, вступление в группы учителей, прохождение ДЗ/контрольных и запуск **самостоятельных пробников КЕГЭ**.
  - В Банке задач: после успешного решения (`is_correct = true`) **ИЛИ после 3 неудачных попыток** (`total_attempts >= 3`) ученику открываются правильный ответ и авторский разбор (`solution_md` + `solution_code`).
  - **Античит-правило Банка во время пробника**: пока у пользователя есть активная незавершённая сессия пробника (`mock_exam`) или контрольного теста (`proctored_test`), все задачи этого варианта **скрываются из выдачи и поиска Банка задач** для этого аккаунта на уровне SQL-запроса бэкенда.
  - **Скрытие источника (`source`)**: в любых назначенных работах (ДЗ, классная работа, тест, пробник) поле `source` (например, *«СтатГрад 12.02.2026»*) не сериализуется бэкендом в JSON до момента завершения работы учеником.

### 1.3. Ролевая модель и переключение контекста (View Mode)
В системе 3 системные роли (`user_role` в БД):
1. **`student`** — базовый пользователь после регистрации через OAuth.
2. **`teacher`** — пользователь с активной (или истёкшей) учительской подпиской либо выданной администратором ролью.
3. **`superuser`** — владелец/администратор платформы.

**Переключение режимов в шапке сайта (`ViewModeSwitcher`):**
- У `teacher` в верхнем хедере есть быстрый переключатель режима интерфейса: **`[Ученик | Учитель]`**. В режиме «Ученик» учитель видит сайт глазами ученика и может сам решать задачи/пробники.
- У `superuser` переключатель: **`[Ученик | Учитель | Админ]`** + инструмент **Impersonation («Смотреть как пользователь»)**: выбор любого юзера по нику/ФИ/UUID, при котором фронтенд добавляет заголовок `X-Impersonate-User: <uuid>` (доступно строго роли `superuser`), позволяя отлаживать группы и статистику глазами конкретного человека.
- Данные профиля пользователя минималистичны и строги: `first_name` (Имя), `last_name` (Фамилия — **ФИ без отчества**), `username` (уникальный никнейм), **без аватарок**.

### 1.4. Монетизация, Подписки и Read-Only деградация
- Оплата на данном этапе происходит вне сайта (лично / через Telegram), эквайринг в код не зашивается.
- В Админке (`/admin/subscriptions`) настраиваются **Тарифы (`subscription_tiers`)**: название, описание, отображаемая цена, публичность на витрине и JSON-набор привилегий (`max_rooms`, `max_students_per_room`, `custom_tasks_enabled`, `proctoring_enabled`, `live_code_watch_enabled`, `mcp_enabled`).
- Выдача подписок работает двумя способами:
  1. Прямое назначение подписки конкретному пользователю на N дней из админки.
  2. Генерация **инвайт-ссылки** (`/activate/:code`) с заданным тарифом, длительностью подписки (например, 30 / 90 / 365 дней) и лимитом активаций (`max_uses`). При активации ссылки пользователь с ролью `student` автоматически повышается до `teacher` и получает подписку.
- **Правило истёкшей подписки (Graceful Read-Only)**: если у учителя истекла подписка, его роль `teacher` и все его группы/результаты учеников **сохраняются навсегда**. Группы переходят в режим **«Только чтение»**: учитель может просматривать прошлые работы, код и таймлайны учеников, но не может создавать новые комнаты, приглашать новых учеников или публиковать новые работы до продления подписки.

### 1.5. Мобильная и Десктопная адаптивность + Античит
- **Мобильные устройства (`< 768px`)**:
  - Полностью работают: Банк задач (чтение и ввод ответов), Главная ученика, Группы, ЛК учителя (включая просмотр live-таблицы и таймлайнов с телефона!), Админка и Авторизация.
  - **Отключены** на мобильных: плавающая Python IDE, полноэкранный Табличный процессор (Excel) и холст Рисовалки (`Overlay`).
- **Античит F12 (`devtools-detector`)**:
  - В продакшене (`DEBUG = false`) открытие браузерных DevTools блокирует рендер приложения экраном-заглушкой `DevtoolsPage` на всём сайте (кроме роли `superuser`). Если DevTools открыт во время активной тестовой сессии, на бэкенд перед блокировкой уходит событие `devtools_triggered`.

---

## 2. Структура монорепозитория

Корень репозитория (`egeinf/`):

```text
egeinf/
├── Project.md                  # Общая архитектура, бизнес-правила и деплой
├── Contract.md                 # Контракт API + WebSockets + JSON-схемы
├── Backend.md                  # Спецификация Go-бэкенда и PostgreSQL 17
├── Frontend.md                 # Спецификация SPA-фронтенда
├── docker-compose.dev.yml      # Инфраструктура для локальной разработки (Postgres, Redis, MinIO)
├── docker-compose.prod.yml     # Полный стек для деплоя на VPS
├── .env.example                # Шаблон переменных окружения
├── deploy/
│   └── nginx/
│       └── default.conf        # Конфиг внутреннего Nginx (COOP/COEP, WS Upgrade, S3 proxy, SPA fallback)
├── db/
│   └── migrations/             # Версионированные SQL-миграции Goose (00001_init.sql и т.д.)
├── backend/                    # Go 1.24+ сервис (chi, pgx/v5, go-redis, minio-go, coder/websocket)
│   ├── cmd/api/main.go
│   ├── internal/
│   ├── go.mod
│   └── Dockerfile
└── frontend/                   # React 19 + Vite 8 + TanStack Router/Query + Tailwind v4
    ├── public/
    ├── src/
    ├── package.json
    └── Dockerfile
```

> Старые разрозненные `.sql` файлы в папке `db/*.sql` заменяются чистой папкой `db/migrations/` (субагент Backend переносит и адаптирует всю полезную логику триггеров в миграции v2 согласно `Backend.md`, после чего архивирует или удаляет устаревшие файлы с `pgvector`/`pg_cron`/`subject`).

---

## 3. Сетевая топология, Docker и Деплой на VPS

### 3.1. Схема проксирования и Single-Origin (Критично для `SharedArrayBuffer`!)
Для работы синхронного `input()` и кнопки «Остановить» в браузерном Python (`Pyodide` в Web Worker) браузер обязан находиться в состоянии `window.crossOriginIsolated === true`. Это требует заголовков:
- `Cross-Origin-Opener-Policy: same-origin`
- `Cross-Origin-Embedder-Policy: require-corp`

Из-за `COEP: require-corp` любые запросы к чужим доменам без `Cross-Origin-Resource-Policy: cross-origin` блокируются браузером. Поэтому **весь трафик проекта маршрутизируется через единый домен**:

```text
Интернет (HTTPS 443)
   │
   ▼
[Внешний Nginx Proxy Manager на VPS] (SSL Termination, проксирует на порт проекта, напр. 8080, с поддержкой WebSockets)
   │
   ▼
[Внутренний контейнер nginx проекта (порт 80 внутри сети проекта)]
   ├── /                  ──► Раздача собранной статики фронтенда (/usr/share/nginx/html) + SPA fallback на /index.html
   │                          (включая локально размещённые колёса Pyodide и wasm-модуль Ruff!)
   ├── /api/v1/*          ──► proxy_pass http://backend:8000; (REST API, передача Cookie и X-Real-IP)
   ├── /ws                ──► proxy_pass http://backend:8000/ws; (Upgrade: websocket, read_timeout 3600s)
   └── /storage/*         ──► proxy_pass http://minio:9000/egeinf-public/*; (публичное чтение картинок и файлов задач с заголовком Cross-Origin-Resource-Policy: same-origin)
```

> **Важный нюанс OAuth при COOP**: так как на основном сайте стоит `COOP: same-origin`, редирект на Яндекс OAuth / VK ID и обратно на `/api/v1/auth/oauth/{provider}/callback` делается **полностраничным top-level редиректом** (`window.location.href = ...`), а НЕ через `window.open` popup (иначе `window.opener` будет `null`).

### 3.2. Эталонный конфиг `deploy/nginx/default.conf`
```nginx
server {
    listen 80;
    server_name _;
    client_max_body_size 100M;

    # Обязательные заголовки для SharedArrayBuffer (Pyodide)
    add_header Cross-Origin-Opener-Policy "same-origin" always;
    add_header Cross-Origin-Embedder-Policy "require-corp" always;
    add_header Cross-Origin-Resource-Policy "same-origin" always;

    # Сжатие статики и больших .txt/.wasm файлов
    gzip on;
    gzip_types text/plain text/css application/json application/javascript application/wasm text/csv;

    # 1. REST API -> Go Backend
    location /api/ {
        proxy_pass http://backend:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;
    }

    # 2. WebSockets -> Go Backend
    location /ws {
        proxy_pass http://backend:8000/ws;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # 3. Файлы и изображения задач из MinIO (публичный бакет)
    location /storage/ {
        proxy_pass http://minio:9000/egeinf-media/;
        proxy_hide_header x-amz-id-2;
        proxy_hide_header x-amz-request-id;
        expires 30d;
        add_header Cache-Control "public, immutable";
        add_header Cross-Origin-Resource-Policy "same-origin" always;
    }

    # 4. Фронтенд статика + SPA роутинг
    location / {
        root /usr/share/nginx/html;
        index index.html;
        try_files $uri $uri/ /index.html;
    }
}
```

### 3.3. Организация бакетов MinIO (S3)
Создаются **два бакета** при старте Go-приложения (автоинициализация через `minio-go`):
1. **`egeinf-media` (Public Read через `/storage/*`)**:
   - `tasks/images/{uuid}.{webp|png|jpg}` — иллюстрации в условиях и разборах задач.
   - `tasks/files/{task_id}/{filename}` — прикреплённые файлы задач (`17.txt`, `9.xlsx`, `27_A.txt`, `27_B.txt`). Сохраняем оригинальное расширение, а при скачивании отдаём понятное имя.
2. **`egeinf-private` (Строго закрытый бакет, доступ только через Go Backend API или Presigned URLs)**:
   - `rooms/{room_id}/free_code/{user_id}.py` — код ученика в свободном редакторе комнаты.
   - `sessions/{session_id}/tasks/{task_id}.py` — итоговый/автосохранённый код ученика по задаче внутри работы или пробника.
   - `imports/temp/{job_id}.zip` — временные архивы при массовом импорте задач.

### 3.4. Локальная разработка (`docker-compose.dev.yml`) vs Продакшен (`docker-compose.prod.yml`)
- **В разработке (Windows / Linux / macOS)**:
  - В Docker поднимаются только `postgres:17-alpine` (порт `5432`), `redis:7-alpine` (порт `6379`) и `minio/minio` (порты `9000` API, `9001` Console).
  - Go-бэкенд запускается нативно (`go run ./cmd/api`) на `localhost:8000`.
  - Vite запускается нативно (`npm run dev`) и в `vite.config.ts` имеет встроенный `server.proxy` для `/api` → `http://localhost:8000`, `/ws` → `ws://localhost:8000`, `/storage` → `http://localhost:9000/egeinf-media`. Таким образом даже локально сохраняется 100% Single-Origin поведение, идентичное продакшен-Nginx!
- **В продакшене (`docker-compose.prod.yml`)**:
  - Все 5 сервисов (`nginx`, `backend`, `postgres`, `redis`, `minio`) объединены во внутреннюю сеть `egeinf_internal`. Наружу для Nginx Proxy Manager торчит только порт внутреннего `nginx` (например, `127.0.0.1:8085:80`).

### 3.5. Переменные окружения (`.env.example`)
```env
# App
APP_ENV=development # development | production
APP_BASE_URL=http://localhost:5173
BACKEND_PORT=8000

# PostgreSQL 17
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=egeinf
POSTGRES_PASSWORD=change_me_strong_password
POSTGRES_DB=egeinf
POSTGRES_SSLMODE=disable

# Redis 7
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0

# MinIO (S3)
S3_ENDPOINT=localhost:9000
S3_ACCESS_KEY=minioadmin
S3_SECRET_KEY=minioadmin_secret
S3_USE_SSL=false
S3_MEDIA_BUCKET=egeinf-media
S3_PRIVATE_BUCKET=egeinf-private

# Auth & JWT (HttpOnly Cookies)
JWT_SECRET=super_secret_32_bytes_minimum_key_here
JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=720h
COOKIE_DOMAIN=
COOKIE_SECURE=false # true in production

# OAuth Providers
YANDEX_CLIENT_ID=
YANDEX_CLIENT_SECRET=
YANDEX_REDIRECT_URL=http://localhost:5173/api/v1/auth/oauth/yandex/callback

VK_CLIENT_ID=
VK_CLIENT_SECRET=
VK_REDIRECT_URL=http://localhost:5173/api/v1/auth/oauth/vk/callback

# Superuser Bootstrap (автоматическое повышение при первом входе по OAuth ID или username)
BOOTSTRAP_SUPERUSER_YANDEX_ID=
BOOTSTRAP_SUPERUSER_VK_ID=
```

---

## 4. Регламент параллельной работы субагентов (Multi-Agent Workflow)

Чтобы субагенты могли писать проект одновременно и независимо друг от друга, соблюдаются следующие границы ответственности:

1. **Единый закон — `Contract.md`**:
   - Ни Backend-агент, ни Frontend-агент **не имеют права самовольно менять имена полей JSON, URL-пути, коды статусов или названия типов событий WebSocket**, описанных в `Contract.md`.
   - Все даты передаются в `ISO-8601 UTC` (`2026-11-12T15:04:05Z`), все идентификаторы пользователей, групп, работ и сессий — `UUID v4`, идентификаторы задач и тем — `int` (`int32`/`int64`).
2. **Зона ответственности Субагента Инфраструктуры / Backend (`Backend.md`)**:
   - Пишет только в `backend/`, `db/migrations/`, `deploy/`, `docker-compose.*.yml`, `.env.example`.
   - Не трогает папку `frontend/`.
   - Предоставляет команду сидирования тестовых данных (`go run ./cmd/seed`), создающую демо-задачи всех типов (обычная, табличная №25, связка №19–21, задача с `.txt`/`.xlsx` файлами), тестового учителя, группу и шкалу перевода баллов КЕГЭ.
3. **Зона ответственности Субагента Frontend (`Frontend.md`)**:
   - Пишет только в `frontend/`.
   - Настраивает в `vite.config.ts` прокси на `:8000` и `:9000`, генерирует строгие TypeScript-типы один-в-один по `Contract.md` (`src/api/types.ts`) и может тестировать сложные экраны на типизированных MSW/моках до запуска бэкенда, а при запущенном бэкенде работает напрямую с ним без переписывания кода.
4. **Рекомендуемое разбиение на параллельных субагентов при старте разработки**:
   - **Agent 1 (DB & Infra + Core Go Backend)**: Docker, миграции БД v2, Auth/OAuth/JWT, Users, Subscriptions/Invites, S3, Фоновые воркеры.
   - **Agent 2 (Go Domain & Realtime Backend)**: Tasks CRUD + CSV/ZIP Importer, Bank & Recommendations API, Rooms & Assignments, Exam Engine (подсчёт баллов 29→100), Двухуровневый WebSocket Hub + батчинг телеметрии.
   - **Agent 3 (Frontend Core, Tools & Student Experience)**: Рефакторинг под TanStack Router/Query, Header + переключение ролей, Pyodide 0.27 + OPFS файловый менеджер, Полноэкранный Excel (`Univer`), Рендерер `TaskCard` (Markdown+KaTeX, связка 19-21, таблицы), Банк задач, Главная и Симулятор Пробника.
   - **Agent 4 (Frontend Teacher Live-LMS & Admin Studio)**: Группы (инвайты + QR), Конструктор работ, Live-матрица класса по WebSocket, Детальное окно ученика (интерактивный Таймлайн + Live-превью кода с подсветкой `paste_ranges`), Админка (визуальный редактор задач, CSV/ZIP импортёр, управление тарифами и инвайтами).

