create table topic
(
    id         serial primary key,
    name       varchar(250) unique not null,
    type       varchar(10)         not null,
    subject_id int                 not null,
    foreign key (subject_id) references subject (id) on delete cascade,
    updated_at timestamptz         not null default now(),
    created_at timestamptz         not null default now(),
    unique (id, subject_id)
);

COMMENT ON TABLE topic IS
    'Темы внутри предмета (например: "Графы", "Рекурсия", "Системы счисления" в Информатике). '
    'Средний уровень иерархии: subject → topic → task. '
    'Используется как ось аналитики: user_topic_stat хранит прогресс пользователя по каждой теме. '
    'При удалении предмета все его темы удаляются каскадно. '
    'Поле type позволяет классифицировать темы (например: теоретическая / практическая).';

COMMENT ON COLUMN topic.id IS 'Суррогатный первичный ключ темы (serial).';
COMMENT ON COLUMN topic.name IS 'Уникальное название темы. Уникально глобально (не только в рамках предмета).';
COMMENT ON COLUMN topic.type IS 'Тип темы — классификатор до 10 символов. Например: "theory", "practice". Используется для фильтрации и группировки задач.';
COMMENT ON COLUMN topic.subject_id IS 'FK на предмет (subject). Тема всегда принадлежит одному предмету. Вместе с id входит в составной уникальный ключ (id, subject_id), на который ссылается FK в таблице task — гарантирует, что задача принадлежит той же теме и предмету одновременно.';
COMMENT ON COLUMN topic.updated_at IS 'Время последнего изменения. Обновляется триггером topic_updated_at.';
COMMENT ON COLUMN topic.created_at IS 'Время создания темы.';

create index topic_name_index on topic (name);
COMMENT ON INDEX topic_name_index IS 'Btree-индекс по имени темы. Ускоряет поиск тем по названию без фильтра по предмету (например, автодополнение в интерфейсе).';

create trigger topic_updated_at
    before update on topic
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER topic_updated_at ON topic IS
    'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';