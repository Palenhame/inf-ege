create table subject
(
    id         serial primary key,
    name       varchar(250) not null unique,
    updated_at timestamptz  not null default now(),
    created_at timestamptz  not null default now()
);

COMMENT ON TABLE subject IS
    'Учебные предметы ЕГЭ (например: Информатика, Математика, Физика). '
    'Верхний уровень иерархии контента: subject → topic → task. '
    'Название предмета уникально глобально.';

COMMENT ON COLUMN subject.id IS 'Суррогатный первичный ключ предмета (serial).';
COMMENT ON COLUMN subject.name IS 'Уникальное название предмета. Например: "Информатика", "Математика".';
COMMENT ON COLUMN subject.updated_at IS 'Время последнего изменения. Обновляется триггером subject_updated_at.';
COMMENT ON COLUMN subject.created_at IS 'Время создания предмета.';

create trigger subject_updated_at
    before update on subject
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER subject_updated_at ON subject IS
    'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';