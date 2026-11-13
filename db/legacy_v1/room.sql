create table room
(
    id          uuid primary key      default gen_random_uuid(),
    name        varchar(250) not null,
    creator_id  uuid         not null,
    foreign key (creator_id) references "user" (id) on delete restrict,
    max_members integer      not null default 20 check ( max_members > 0 and max_members <= 40 ),
    updated_at  timestamptz  not null default now(),
    created_at  timestamptz  not null default now()
);

COMMENT ON TABLE room IS
    'Учебная комната — пространство учителя для проведения тестов и совместной работы. '
    'Учитель создаёт комнату и приглашает учеников (user_room). '
    'Внутри комнаты создаются тесты (test). '
    'При удалении создателя комната сохраняется (ON DELETE RESTRICT) — '
    'содержимое комнаты не должно теряться при смене учителя. '
    'Один учитель не может создать две комнаты с одинаковым именем.';

COMMENT ON COLUMN room.id IS 'UUID комнаты, генерируется автоматически.';
COMMENT ON COLUMN room.name IS 'Название комнаты. Уникально в рамках одного создателя (creator_id + name).';
COMMENT ON COLUMN room.creator_id IS 'FK на пользователя-создателя (учителя). ON DELETE RESTRICT — удаление учителя запрещено при наличии комнат.';
COMMENT ON COLUMN room.max_members IS 'Максимальное количество участников комнаты (1–40, по умолчанию 20). Контролируется приложением при добавлении участников в user_room.';
COMMENT ON COLUMN room.updated_at IS 'Время последнего изменения комнаты. Обновляется триггером room_updated_at. Используется для сортировки комнат по активности.';
COMMENT ON COLUMN room.created_at IS 'Время создания комнаты.';

create index room_name_index on room (name);
COMMENT ON INDEX room_name_index IS 'Btree-индекс по name. Ускоряет поиск комнат по названию без фильтра по создателю.';

create index room_updated_at_index on room (updated_at);
COMMENT ON INDEX room_updated_at_index IS 'Btree-индекс по updated_at. Ускоряет сортировку комнат по активности (список "последних активных комнат" учителя).';

create trigger room_updated_at
    before update on room
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER room_updated_at ON room IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';