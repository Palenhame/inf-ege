create table "user"
(
    id         uuid primary key             default gen_random_uuid(),
    username   varchar(250) unique not null,
    yandex_id  varchar(250) unique null,
    vk_id      varchar(250) unique null,
    role       user_role           not null default 'user',
    updated_at timestamptz         not null default now(),
    created_at timestamptz         not null default now()
);

COMMENT ON TABLE "user" IS
    'Аккаунты пользователей платформы. Аутентификация через OAuth: Яндекс ID или VK ID. '
    'Пользователь может привязать один или оба провайдера, либо не привязывать ни одного '
    '(аутентификация через username/password — расширяется приложением). '
    'Роль определяет доступные действия в системе (user_role).';

COMMENT ON COLUMN "user".id IS 'UUID пользователя, генерируется автоматически. Используется как FK во всех связанных таблицах.';
COMMENT ON COLUMN "user".username IS 'Уникальное отображаемое имя пользователя. Не может совпадать с другими аккаунтами.';
COMMENT ON COLUMN "user".yandex_id IS 'Идентификатор аккаунта Яндекс OAuth. NULL если провайдер не привязан. Уникален глобально.';
COMMENT ON COLUMN "user".vk_id IS 'Идентификатор аккаунта VK OAuth. NULL если провайдер не привязан. Уникален глобально.';
COMMENT ON COLUMN "user".role IS 'Роль пользователя: superuser / moderator / user. Определяет права доступа к управлению контентом и административным функциям.';
COMMENT ON COLUMN "user".updated_at IS 'Время последнего изменения записи. Обновляется триггером user_updated_at автоматически. Используется для audit-трейла.';
COMMENT ON COLUMN "user".created_at IS 'Время регистрации аккаунта.';

create trigger user_updated_at
    before update on "user"
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER user_updated_at ON "user" IS
    'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';