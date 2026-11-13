package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
)

// mockDB is a test double for DB.
type mockDB struct {
	mu         sync.Mutex
	execCalls  []string
	queryCalls []string
	execFn     func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (m *mockDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	m.mu.Lock()
	m.execCalls = append(m.execCalls, sql)
	m.mu.Unlock()

	if m.execFn != nil {
		return m.execFn(ctx, sql, arguments...)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	m.mu.Lock()
	m.queryCalls = append(m.queryCalls, sql)
	m.mu.Unlock()

	if m.queryFn != nil {
		return m.queryFn(ctx, sql, args...)
	}
	return &emptyRows{}, nil
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	m.mu.Lock()
	m.queryCalls = append(m.queryCalls, sql)
	m.mu.Unlock()

	if m.queryRowFn != nil {
		return m.queryRowFn(ctx, sql, args...)
	}
	return &mockRow{}
}

// emptyRows implements an empty pgx.Rows.
type emptyRows struct{}

func (e *emptyRows) Close()                                       {}
func (e *emptyRows) Err() error                                   { return nil }
func (e *emptyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (e *emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (e *emptyRows) Next() bool                                   { return false }
func (e *emptyRows) Scan(dest ...any) error                       { return nil }
func (e *emptyRows) Values() ([]any, error)                       { return nil, nil }
func (e *emptyRows) RawValues() [][]byte                          { return nil }
func (e *emptyRows) Conn() *pgx.Conn                              { return nil }
func (e *emptyRows) TypeMap() *pgtype.Map                         { return pgtype.NewMap() }

// sliceRows implements pgx.Rows over an in-memory slice of scanned rows.
type sliceRows struct {
	rows    [][]any
	current int
	closed  bool
	err     error
}

func newSliceRows(rows [][]any) *sliceRows {
	return &sliceRows{rows: rows, current: -1}
}

func (s *sliceRows) Close()                                       { s.closed = true }
func (s *sliceRows) Err() error                                   { return s.err }
func (s *sliceRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (s *sliceRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (s *sliceRows) Next() bool {
	s.current++
	return s.current < len(s.rows)
}

func (s *sliceRows) Scan(dest ...any) error {
	if s.current < 0 || s.current >= len(s.rows) {
		return fmt.Errorf("no row to scan")
	}
	row := s.rows[s.current]
	for i, val := range row {
		if i >= len(dest) {
			break
		}
		switch d := dest[i].(type) {
		case *uuid.UUID:
			if u, ok := val.(uuid.UUID); ok {
				*d = u
			} else if str, ok := val.(string); ok {
				parsed, _ := uuid.Parse(str)
				*d = parsed
			}
		case *string:
			if val == nil {
				*d = ""
			} else {
				*d = fmt.Sprint(val)
			}
		case **string:
			if val == nil {
				*d = nil
			} else if str, ok := val.(string); ok {
				*d = &str
			} else {
				v := fmt.Sprint(val)
				*d = &v
			}
		case *int16:
			if v, ok := val.(int16); ok {
				*d = v
			} else if v, ok := val.(int); ok {
				*d = int16(v)
			}
		case **int16:
			if val == nil {
				*d = nil
			} else if p, ok := val.(*int16); ok {
				*d = p
			} else if v, ok := val.(int16); ok {
				*d = &v
			} else if v, ok := val.(int); ok {
				i16 := int16(v)
				*d = &i16
			}
		case *time.Time:
			if t, ok := val.(time.Time); ok {
				*d = t
			}
		default:
			assignScan(d, val)
		}
	}
	return nil
}

func assignScan(target any, val any) {
	if scanner, ok := target.(interface{ Scan(any) error }); ok {
		_ = scanner.Scan(val)
	}
}

func (s *sliceRows) Values() ([]any, error) { return s.rows[s.current], nil }
func (s *sliceRows) RawValues() [][]byte   { return nil }
func (s *sliceRows) Conn() *pgx.Conn       { return nil }
func (s *sliceRows) TypeMap() *pgtype.Map  { return pgtype.NewMap() }

// mockRow implements pgx.Row.
type mockRow struct {
	values []any
	err    error
}

func (m *mockRow) Scan(dest ...any) error {
	if m.err != nil {
		return m.err
	}
	for i, d := range dest {
		if i < len(m.values) {
			switch target := d.(type) {
			case *float64:
				if v, ok := m.values[i].(float64); ok {
					*target = v
				}
			case *string:
				if v, ok := m.values[i].(string); ok {
					*target = v
				}
			case *int:
				if v, ok := m.values[i].(int); ok {
					*target = v
				}
			}
		}
	}
	return nil
}

// mockRedisClient implements RedisClient in-memory.
type mockRedisClient struct {
	mu     sync.Mutex
	data   map[string]string
	expiry map[string]time.Time
}

func newMockRedisClient() *mockRedisClient {
	return &mockRedisClient{
		data:   make(map[string]string),
		expiry: make(map[string]time.Time),
	}
}

func (m *mockRedisClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewBoolCmd(ctx)
	if exp, exists := m.expiry[key]; exists && time.Now().After(exp) {
		delete(m.data, key)
		delete(m.expiry, key)
	}

	if _, exists := m.data[key]; exists {
		cmd.SetVal(false)
		return cmd
	}

	m.data[key] = fmt.Sprint(value)
	if expiration > 0 {
		m.expiry[key] = time.Now().Add(expiration)
	}
	cmd.SetVal(true)
	return cmd
}

func (m *mockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewStringCmd(ctx)
	if exp, exists := m.expiry[key]; exists && time.Now().After(exp) {
		delete(m.data, key)
		delete(m.expiry, key)
	}

	if val, exists := m.data[key]; exists {
		cmd.SetVal(val)
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (m *mockRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewStatusCmd(ctx)
	m.data[key] = fmt.Sprint(value)
	if expiration > 0 {
		m.expiry[key] = time.Now().Add(expiration)
	}
	cmd.SetVal("OK")
	return cmd
}

func (m *mockRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewCmd(ctx)
	if len(keys) > 0 && len(args) > 0 {
		key := keys[0]
		expected := fmt.Sprint(args[0])
		if current, exists := m.data[key]; exists && current == expected {
			delete(m.data, key)
			delete(m.expiry, key)
			cmd.SetVal(int64(1))
			return cmd
		}
	}
	cmd.SetVal(int64(0))
	return cmd
}

func (m *mockRedisClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewIntCmd(ctx)
	var count int64
	for _, k := range keys {
		if _, exists := m.data[k]; exists {
			delete(m.data, k)
			delete(m.expiry, k)
			count++
		}
	}
	cmd.SetVal(count)
	return cmd
}
