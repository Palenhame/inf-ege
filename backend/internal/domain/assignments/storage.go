package assignments

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"backend/internal/platform/storage"
	"github.com/minio/minio-go/v7"
)

// CodeStorage defines the abstraction for storing student code snapshots.
type CodeStorage interface {
	SaveCode(ctx context.Context, sessionID string, taskID int, code string) (string, error)
	GetCode(ctx context.Context, s3Key string) (string, error)
}

// PlatformCodeStorage implements CodeStorage using the platform storage.Storage wrapper.
type PlatformCodeStorage struct {
	storage *storage.Storage
}

// NewPlatformCodeStorage constructs a CodeStorage using platform storage.
func NewPlatformCodeStorage(s *storage.Storage) *PlatformCodeStorage {
	return &PlatformCodeStorage{storage: s}
}

// SaveCode writes code to the private bucket.
func (p *PlatformCodeStorage) SaveCode(ctx context.Context, sessionID string, taskID int, code string) (string, error) {
	key := fmt.Sprintf("sessions/%s/tasks/%d.py", sessionID, taskID)
	if p.storage == nil {
		return key, nil
	}
	_, err := p.storage.UploadBytes(ctx, p.storage.PrivateBucket(), key, []byte(code), "text/x-python; charset=utf-8")
	if err != nil {
		return "", fmt.Errorf("platform storage upload: %w", err)
	}
	return key, nil
}

// GetCode reads code from the private bucket.
func (p *PlatformCodeStorage) GetCode(ctx context.Context, s3Key string) (string, error) {
	if p.storage == nil || s3Key == "" {
		return "", nil
	}
	data, err := p.storage.DownloadBytes(ctx, p.storage.PrivateBucket(), s3Key)
	if err != nil {
		return "", fmt.Errorf("platform storage download: %w", err)
	}
	return string(data), nil
}

// MinIOCodeStorage implements CodeStorage using a raw MinIO S3 client.
type MinIOCodeStorage struct {
	client     *minio.Client
	bucketName string
}

// NewMinIOCodeStorage constructs a MinIO-backed CodeStorage.
func NewMinIOCodeStorage(client *minio.Client, privateBucket string) *MinIOCodeStorage {
	if privateBucket == "" {
		privateBucket = "egeinf-private"
	}
	return &MinIOCodeStorage{
		client:     client,
		bucketName: privateBucket,
	}
}

// SaveCode writes student python code into MinIO egeinf-private bucket.
func (s *MinIOCodeStorage) SaveCode(ctx context.Context, sessionID string, taskID int, code string) (string, error) {
	key := fmt.Sprintf("sessions/%s/tasks/%d.py", sessionID, taskID)
	data := []byte(code)
	reader := bytes.NewReader(data)

	if s.client == nil {
		return key, nil
	}

	_, err := s.client.PutObject(ctx, s.bucketName, key, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: "text/x-python; charset=utf-8",
	})
	if err != nil {
		return "", fmt.Errorf("minio put object failed: %w", err)
	}
	return key, nil
}

// GetCode retrieves student python code from MinIO egeinf-private bucket.
func (s *MinIOCodeStorage) GetCode(ctx context.Context, s3Key string) (string, error) {
	if s.client == nil || s3Key == "" {
		return "", nil
	}

	obj, err := s.client.GetObject(ctx, s.bucketName, s3Key, minio.GetObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("minio get object failed: %w", err)
	}
	defer obj.Close()

	buf, err := io.ReadAll(obj)
	if err != nil {
		return "", fmt.Errorf("minio read object failed: %w", err)
	}
	return string(buf), nil
}

// MemoryCodeStorage is an in-memory implementation for unit and contract testing.
type MemoryCodeStorage struct {
	mu    sync.RWMutex
	files map[string]string
}

// NewMemoryCodeStorage creates a thread-safe in-memory CodeStorage.
func NewMemoryCodeStorage() *MemoryCodeStorage {
	return &MemoryCodeStorage{
		files: make(map[string]string),
	}
}

// SaveCode records code in memory.
func (m *MemoryCodeStorage) SaveCode(ctx context.Context, sessionID string, taskID int, code string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("sessions/%s/tasks/%d.py", sessionID, taskID)
	m.files[key] = code
	return key, nil
}

// GetCode reads code from memory.
func (m *MemoryCodeStorage) GetCode(ctx context.Context, s3Key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	code, exists := m.files[s3Key]
	if !exists {
		return "", nil
	}
	return code, nil
}
