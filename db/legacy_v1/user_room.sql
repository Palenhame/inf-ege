create table user_room
(
    id           serial primary key,
    user_id      uuid         not null,
    foreign key (user_id) references "user" (id) on delete cascade,
    room_id      uuid         not null,
    foreign key (room_id) references room (id) on delete cascade,
    link_to_code varchar(250) null unique,
    updated_at   timestamptz  not null default now(),
    created_at   timestamptz  not null default now(),
    unique (user_id, room_id)
);

COMMENT ON TABLE user_room IS
    'Членство пользователя (ученика) в комнате. '
    'Одна запись на пару (user_id, room_id). '
    'При удалении пользователя или комнаты запись удаляется каскадно. '
    'link_to_code хранит S3-путь к коду, написанному в свободном редакторе комнаты — '
    'отдельно от кода, сданного в тестах (user_room_test_task.link_to_code).';

COMMENT ON COLUMN user_room.id IS 'Суррогатный первичный ключ (serial). Уникальность пары (user_id, room_id) обеспечивается UNIQUE.';
COMMENT ON COLUMN user_room.user_id IS 'FK на участника комнаты. При удалении пользователя — каскадное удаление.';
COMMENT ON COLUMN user_room.room_id IS 'FK на комнату. При удалении комнаты — каскадное удаление.';
COMMENT ON COLUMN user_room.link_to_code IS 'S3-путь к файлу кода из свободного редактора комнаты: code/{room_id}/{user_id}.py. NULL если пользователь не писал код в свободном редакторе. Уникален глобально — исключает коллизии путей в S3.';
COMMENT ON COLUMN user_room.updated_at IS 'Время последнего изменения членства. Обновляется триггером user_room_updated_at.';
COMMENT ON COLUMN user_room.created_at IS 'Время вступления пользователя в комнату.';

create index user_room_room_id_index on user_room (room_id);
COMMENT ON INDEX user_room_room_id_index IS 'Btree-индекс по room_id. Ускоряет выборку всех участников конкретной комнаты.';

create index user_room_updated_at_index on user_room (updated_at desc);
COMMENT ON INDEX user_room_updated_at_index IS 'Btree-индекс по updated_at DESC. Ускоряет выборку последних активных участников.';

create trigger user_room_updated_at
    before update on user_room
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER user_room_updated_at ON user_room IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';