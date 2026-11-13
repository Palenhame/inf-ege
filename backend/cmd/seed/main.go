package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"backend/internal/config"
	"backend/internal/domain/subscriptions"
	"backend/internal/domain/tasks"
	"backend/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Primary (0..29) to Secondary (0..100) KEGE score conversion table
var kegeScoreScale = [30]int16{
	0: 0, 1: 7, 2: 14, 3: 20, 4: 27, 5: 34, 6: 40, 7: 43, 8: 46, 9: 48,
	10: 51, 11: 54, 12: 56, 13: 59, 14: 62, 15: 64, 16: 67, 17: 70, 18: 72, 19: 75,
	20: 78, 21: 80, 22: 83, 23: 85, 24: 88, 25: 90, 26: 93, 27: 95, 28: 98, 29: 100,
}

type defaultTier struct {
	Slug        string
	Name        string
	Description string
	PriceRub    int
	IsPublic    bool
	SortOrder   int
	Features    subscriptions.TierFeatures
}

var defaultTiers = []defaultTier{
	{
		Slug:        "free",
		Name:        "Бесплатный",
		Description: "Базовый доступ для учеников и индивидуальных занятий",
		PriceRub:    0,
		IsPublic:    true,
		SortOrder:   1,
		Features: subscriptions.TierFeatures{
			MaxRooms:             1,
			MaxStudentsPerRoom:   5,
			CustomTasksEnabled:   false,
			ProctoringEnabled:    false,
			LiveCodeWatchEnabled: false,
			LiveStreamEnabled:    false,
			MCPEnabled:           false,
		},
	},
	{
		Slug:        "standard",
		Name:        "Стандарт",
		Description: "Для репетиторов и небольших классов: до 5 комнат и авторские варианты",
		PriceRub:    990,
		IsPublic:    true,
		SortOrder:   2,
		Features: subscriptions.TierFeatures{
			MaxRooms:             5,
			MaxStudentsPerRoom:   30,
			CustomTasksEnabled:   true,
			ProctoringEnabled:    true,
			LiveCodeWatchEnabled: true,
			LiveStreamEnabled:    true,
			MCPEnabled:           false,
		},
	},
	{
		Slug:        "pro",
		Name:        "ПРО Преподаватель",
		Description: "Полный доступ без ограничений: неограниченные группы, Live-прокторинг и MCP AI",
		PriceRub:    2490,
		IsPublic:    true,
		SortOrder:   3,
		Features: subscriptions.TierFeatures{
			MaxRooms:             100,
			MaxStudentsPerRoom:   100,
			CustomTasksEnabled:   true,
			ProctoringEnabled:    true,
			LiveCodeWatchEnabled: true,
			LiveStreamEnabled:    true,
			MCPEnabled:           true,
		},
	},
}

type kimTopic struct {
	KIMNumber int
	Name      string
	SortOrder int
}

var kimTopics = []kimTopic{
	{1, "Анализ информационных моделей (графы, таблицы)", 1},
	{2, "Построение таблиц истинности логических выражений", 2},
	{3, "Поиск информации в реляционных базах данных", 3},
	{4, "Кодирование и декодирование информации (условие Фано)", 4},
	{5, "Анализ алгоритмов для исполнителей (системы счисления)", 5},
	{6, "Анализ программ с циклами для Чертёжника и Черепахи", 6},
	{7, "Кодирование графической и звуковой информации", 7},
	{8, "Комбинаторика и перебор слов", 8},
	{9, "Обработка числовых таблиц в электронных таблицах", 9},
	{10, "Поиск слова и фрагментов текста в документах", 10},
	{11, "Вычисление количества информации и объёма памяти", 11},
	{12, "Выполнение алгоритмов для исполнителя Редактор", 12},
	{13, "Адресация в сетях TCP/IP (маски подсетей)", 13},
	{14, "Позиционные системы счисления (арифметические выражения)", 14},
	{15, "Истинность логических выражений (множества, отрезки, делимость)", 15},
	{16, "Вычисление значений рекурсивных функций", 16},
	{17, "Обработка целочисленных последовательностей", 17},
	{18, "Динамическое программирование в электронных таблицах (Робот)", 18},
	{19, "Теория игр: поиск выигрышной стратегии (одна куча)", 19},
	{20, "Теория игр: поиск выигрышной стратегии (две кучи) — ход 1", 20},
	{21, "Теория игр: поиск выигрышной стратегии (две кучи) — ход 2", 21},
	{22, "Параллельные процессы и многопоточные вычисления", 22},
	{23, "Динамическое программирование: количество программ", 23},
	{24, "Обработка символьных строк в файле", 24},
	{25, "Обработка целочисленной информации (поиск делителей, маски)", 25},
	{26, "Обработка больших массивов данных (жадные алгоритмы, сортировка)", 26},
	{27, "Анализ больших последовательностей данных (эффективные алгоритмы)", 27},
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger.Info("Starting database seeder...")

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	pgClient, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("Failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pgClient.Close()

	pool := pgClient.Pool

	// 1. Seed exam_score_scale
	if err := seedScoreScale(ctx, pool, logger); err != nil {
		logger.Error("Failed to seed exam score scale", "error", err)
		os.Exit(1)
	}

	// 2. Seed subscription_tiers
	if err := seedSubscriptionTiers(ctx, pool, logger); err != nil {
		logger.Error("Failed to seed subscription tiers", "error", err)
		os.Exit(1)
	}

	// 3. Seed topics 1..27
	topicMap, err := seedTopics(ctx, pool, logger)
	if err != nil {
		logger.Error("Failed to seed topics", "error", err)
		os.Exit(1)
	}

	// 4. Seed demo tasks
	if err := seedDemoTasks(ctx, pool, topicMap, logger); err != nil {
		logger.Error("Failed to seed demo tasks", "error", err)
		os.Exit(1)
	}

	logger.Info("Database seeding finished successfully!")
}

func seedScoreScale(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	const query = `
		INSERT INTO exam_score_scale (primary_score, secondary_score)
		VALUES ($1, $2)
		ON CONFLICT (primary_score) DO UPDATE
		SET secondary_score = EXCLUDED.secondary_score
	`
	for primary, secondary := range kegeScoreScale {
		if _, err := pool.Exec(ctx, query, int16(primary), secondary); err != nil {
			return fmt.Errorf("insert score scale %d: %w", primary, err)
		}
	}

	logger.Info("Seeded exam_score_scale table", "rows", len(kegeScoreScale))
	return nil
}

func seedSubscriptionTiers(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	const query = `
		INSERT INTO subscription_tiers (slug, name, description, price_rub, is_public, sort_order, features)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (slug) DO UPDATE
		SET name = EXCLUDED.name,
		    description = EXCLUDED.description,
		    price_rub = EXCLUDED.price_rub,
		    is_public = EXCLUDED.is_public,
		    sort_order = EXCLUDED.sort_order,
		    features = EXCLUDED.features,
		    updated_at = now()
	`
	for _, tier := range defaultTiers {
		featBytes, err := json.Marshal(tier.Features)
		if err != nil {
			return fmt.Errorf("marshal tier features for %s: %w", tier.Slug, err)
		}

		if _, err := pool.Exec(ctx, query, tier.Slug, tier.Name, tier.Description, tier.PriceRub, tier.IsPublic, tier.SortOrder, featBytes); err != nil {
			return fmt.Errorf("insert subscription tier %s: %w", tier.Slug, err)
		}
	}

	logger.Info("Seeded subscription_tiers table", "tiers_count", len(defaultTiers))
	return nil
}

func seedTopics(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (map[int]int, error) {
	const upsertQuery = `
		INSERT INTO topics (kim_number, name, sort_order)
		VALUES ($1, $2, $3)
		ON CONFLICT (kim_number, name) DO UPDATE
		SET sort_order = EXCLUDED.sort_order,
		    updated_at = now()
		RETURNING id, kim_number
	`

	topicIDByKIM := make(map[int]int)

	for _, t := range kimTopics {
		var id int
		var kimNum int
		err := pool.QueryRow(ctx, upsertQuery, t.KIMNumber, t.Name, t.SortOrder).Scan(&id, &kimNum)
		if err != nil {
			return nil, fmt.Errorf("upsert topic %d: %w", t.KIMNumber, err)
		}
		topicIDByKIM[kimNum] = id
	}

	logger.Info("Seeded topics table (KIM 1..27)", "topics_count", len(kimTopics))
	return topicIDByKIM, nil
}

func seedDemoTasks(ctx context.Context, pool *pgxpool.Pool, topicMap map[int]int, logger *slog.Logger) error {
	type demoTask struct {
		Source       string
		KIMNumbers   []int16
		TopicKIM     int
		Difficulty   int16
		TextMD       string
		Images       []string
		Files        []tasks.FileAttachment
		AnswerConfig tasks.AnswerConfig
		CorrectAnswer tasks.CorrectAnswer
		SolutionMD   string
		SolutionCode string
	}

	demoList := []demoTask{
		// 1. Single answer task (KIM 1)
		{
			Source:     "Демоверсия ФИПИ КЕГЭ 2026",
			KIMNumbers: []int16{1},
			TopicKIM:   1,
			Difficulty: 1,
			TextMD: `На рисунке справа схема дорог Н-ского района изображена в виде графа, в таблице содержатся сведения о длинах этих дорог (в километрах).

Так как таблицу и схему рисовали независимо друг от друга, то нумерация населённых пунктов в таблице никак не связана с буквенными обозначениями на графе.

Определите, какова сумма протяжённостей дорог из пункта B в пункт C и из пункта D в пункт F. В ответе запишите целое число.`,
			Images: []string{},
			Files:  []tasks.FileAttachment{},
			AnswerConfig: tasks.AnswerConfig{
				AnswerType:     "single",
				MaxScore:       1,
				TrimWhitespace: true,
			},
			CorrectAnswer: tasks.CorrectAnswer{
				AnswerType: "single",
				Accepted:   []string{"42"},
			},
			SolutionMD:   "Степень вершины В равна 2, вершины С — 3. Сопоставляя строки таблицы со степенями вершин графа, находим длины соответствующих рёбер и их сумму: 42.",
			SolutionCode: "print(42)",
		},
		// 2. Pair answer task (KIM 25)
		{
			Source:     "ФИПИ КЕГЭ 2026",
			KIMNumbers: []int16{25},
			TopicKIM:   25,
			Difficulty: 2,
			TextMD: `Назовём маской числа последовательность цифр, в которой могут встречаться спецсимволы '?' и '*'.
Найдите на отрезке [500000; 500100] все числа, имеющие ровно два нетривиальных делителя.

В качестве ответа запишите наименьшее и наибольшее найденные числа через пробел (или в соответствующие поля).`,
			Images: []string{},
			Files:  []tasks.FileAttachment{},
			AnswerConfig: tasks.AnswerConfig{
				AnswerType:     "pair",
				MaxScore:       2,
				Labels:         []string{"Наименьшее число", "Наибольшее число"},
				ScoringRule:    "per_item",
				TrimWhitespace: true,
			},
			CorrectAnswer: tasks.CorrectAnswer{
				AnswerType: "pair",
				Values:     []string{"500010", "500095"},
			},
			SolutionMD: "Перебираем числа в цикле и проверяем количество делителей с помощью перебора до sqrt(n).",
			SolutionCode: `def get_divs(n):
    d = []
    for i in range(2, int(n**0.5) + 1):
        if n % i == 0:
            d.append(i)
            if i * i != n:
                d.append(n // i)
    return sorted(d)

found = []
for x in range(500000, 500101):
    if len(get_divs(x)) == 2:
        found.append(x)
print(found[0], found[-1])`,
		},
		// 3. Table answer task (KIM 26)
		{
			Source:     "СтатГрад КЕГЭ 2026",
			KIMNumbers: []int16{26},
			TopicKIM:   26,
			Difficulty: 3,
			TextMD: `В обувной магазин поступили коробки с обувью разного размера. Покупатель хочет выбрать максимальное количество коробок, вкладывая одну в другую, как матрёшку. Разность размеров каждой последующей вложенной коробки должна составлять не менее 3 единиц.

В первой строке запишите максимальное количество коробок, которое можно вложить друг в друга. Во второй строке запишите максимально возможный размер самой маленькой коробки в такой цепочке.`,
			Images: []string{},
			Files:  []tasks.FileAttachment{},
			AnswerConfig: tasks.AnswerConfig{
				AnswerType:     "table",
				MaxScore:       2,
				Rows:           2,
				Cols:           1,
				ColHeaders:     []string{"Значение"},
				ScoringRule:    "exact_only",
				TrimWhitespace: true,
			},
			CorrectAnswer: tasks.CorrectAnswer{
				AnswerType: "table",
				Matrix: [][]string{
					{"58"},
					{"124"},
				},
			},
			SolutionMD: "Жадный алгоритм: сортируем коробки по убыванию размера. На каждом шаге выбираем коробку максимально возможного размера, удовлетворяющую ограничению разницы >= 3.",
			SolutionCode: `boxes = sorted([int(x) for x in open('26.txt')], reverse=True)
res = [boxes[0]]
for b in boxes[1:]:
    if res[-1] - b >= 3:
        res.append(b)
print(len(res), res[-1])`,
		},
		// 4. Triplet answer task (KIM 19, 20, 21)
		{
			Source:     "Демоверсия ФИПИ КЕГЭ 2026",
			KIMNumbers: []int16{19, 20, 21},
			TopicKIM:   19,
			Difficulty: 3,
			TextMD: `Два игрока, Петя и Ваня, играют в следующую игру перед одной кучей камней. Игроки ходят по очереди, первый ход делает Петя. За один ход игрок может добавить один камень (+1) или увеличить количество камней в два раза (*2). Игра завершается, когда количество камней становится не менее 129. Начальное количество камней S (1 <= S <= 128).

Задание 19: Укажите минимальное значение S, при котором Петя не может выиграть за один ход, но при любом ходе Пети Ваня может выиграть своим первым ходом.
Задание 20: Найдите два наименьших значения S, при которых у Пети есть выигрышная стратегия за 2 хода, но нет за 1 ход.
Задание 21: Найдите значение S, при котором у Вани есть выигрышная стратегия первым или вторым ходом.`,
			Images: []string{},
			Files:  []tasks.FileAttachment{},
			AnswerConfig: tasks.AnswerConfig{
				AnswerType: "triplet_19_21",
				MaxScore:   3,
				Items: []tasks.TripletItemConfig{
					{Kim: 19, Kind: "single", Label: "Задание 19"},
					{Kim: 20, Kind: "pair", Label: "Задание 20"},
					{Kim: 21, Kind: "single", Label: "Задание 21"},
				},
				TrimWhitespace: true,
			},
			CorrectAnswer: tasks.CorrectAnswer{
				AnswerType: "triplet_19_21",
				Ans19:      []string{"64"},
				Ans20:      []string{"32", "63"},
				Ans21:      []string{"62"},
			},
			SolutionMD: "Рекурсивный анализ ходов игры: для №19 S=64, для №20 S=32, 63, для №21 S=62.",
			SolutionCode: `def moves(s):
    return [s + 1, s * 2]

def game(s):
    if any(m >= 129 for m in moves(s)):
        return 'W1'
    if all(game(m) == 'W1' for m in moves(s)):
        return 'L1'
    if any(game(m) == 'L1' for m in moves(s)):
        return 'W2'
    if all(game(m) in ('W1', 'W2') for m in moves(s)):
        return 'L2'
    return '?'`,
		},
		// 5. Single answer task with file attachment (KIM 24)
		{
			Source:     "Основная волна КЕГЭ 2026",
			KIMNumbers: []int16{24},
			TopicKIM:   24,
			Difficulty: 2,
			TextMD: `Текстовый файл 24.txt содержит строку из заглавных букв латинского алфавита A, B, C, D, E, F длиной до 10^6 символов.

Определите максимальную длину подстроки, не содержащей двух идущих подряд гласных букв (буквы A, E). В ответе укажите найденную длину.`,
			Images: []string{},
			Files: []tasks.FileAttachment{
				{
					ID:        "demo_24_txt",
					Name:      "24.txt",
					URL:       "/storage/media/tasks/demo_24.txt",
					SizeBytes: 1048576,
					Ext:       ".txt",
				},
			},
			AnswerConfig: tasks.AnswerConfig{
				AnswerType:     "single",
				MaxScore:       1,
				TrimWhitespace: true,
			},
			CorrectAnswer: tasks.CorrectAnswer{
				AnswerType: "single",
				Accepted:   []string{"256"},
			},
			SolutionMD: "Заменяем буквы E на A, затем пары AA разделяем пробелом. Искомая длина — максимальная длина слова.",
			SolutionCode: `s = open('24.txt').read()
s = s.replace('E', 'A')
while 'AA' in s:
    s = s.replace('AA', 'A A')
print(max(len(part) for part in s.split()))`,
		},
	}

	for i, d := range demoList {
		topicID, ok := topicMap[d.TopicKIM]
		if !ok || topicID == 0 {
			return fmt.Errorf("topic for KIM %d not found", d.TopicKIM)
		}

		// Check if task with identical text already exists
		var existingID int
		err := pool.QueryRow(ctx, "SELECT id FROM tasks WHERE text_md = $1 LIMIT 1", d.TextMD).Scan(&existingID)
		if err == nil && existingID > 0 {
			logger.Info("Demo task already exists, skipping", "index", i+1, "id", existingID)
			continue
		}

		filesJSON, err := json.Marshal(d.Files)
		if err != nil {
			return fmt.Errorf("marshal files: %w", err)
		}
		ansConfigJSON, err := json.Marshal(d.AnswerConfig)
		if err != nil {
			return fmt.Errorf("marshal answer_config: %w", err)
		}
		correctAnsJSON, err := json.Marshal(d.CorrectAnswer)
		if err != nil {
			return fmt.Errorf("marshal correct_answer: %w", err)
		}

		const insertTaskQuery = `
			INSERT INTO tasks (
				is_public_bank,
				kim_numbers,
				topic_id,
				difficulty,
				source,
				text_md,
				images,
				files,
				answer_config,
				correct_answer,
				solution_md,
				solution_code
			) VALUES (
				true,
				$1,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7,
				$8,
				$9,
				$10,
				$11
			)
			RETURNING id
		`

		var newID int
		err = pool.QueryRow(ctx, insertTaskQuery,
			d.KIMNumbers,
			topicID,
			d.Difficulty,
			d.Source,
			d.TextMD,
			d.Images,
			filesJSON,
			ansConfigJSON,
			correctAnsJSON,
			d.SolutionMD,
			d.SolutionCode,
		).Scan(&newID)
		if err != nil {
			return fmt.Errorf("insert demo task %d: %w", i+1, err)
		}

		logger.Info("Inserted demo task", "index", i+1, "id", newID, "kim", d.KIMNumbers, "answer_type", d.AnswerConfig.AnswerType)
	}

	logger.Info("Seeded demo tasks", "count", len(demoList))
	return nil
}
