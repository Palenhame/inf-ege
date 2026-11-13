package storage

import (
	"context"
	"testing"
	"time"

	"backend/internal/config"
	"github.com/minio/minio-go/v7"
)

func TestStorageHelpers(t *testing.T) {
	storage := Wrap(&minio.Client{}, "egeinf-media", "egeinf-private")

	if storage.MediaBucket() != "egeinf-media" {
		t.Errorf("expected MediaBucket 'egeinf-media', got '%s'", storage.MediaBucket())
	}
	if storage.PrivateBucket() != "egeinf-private" {
		t.Errorf("expected PrivateBucket 'egeinf-private', got '%s'", storage.PrivateBucket())
	}

	testCases := []struct {
		input    string
		expected string
	}{
		{"tasks/images/abc.png", "/storage/tasks/images/abc.png"},
		{"/tasks/images/abc.png", "/storage/tasks/images/abc.png"},
		{"tasks/files/24/data.txt", "/storage/tasks/files/24/data.txt"},
		{"simple.webp", "/storage/simple.webp"},
	}

	for _, tc := range testCases {
		res := storage.PublicURL(tc.input)
		if res != tc.expected {
			t.Errorf("PublicURL(%s): expected '%s', got '%s'", tc.input, tc.expected, res)
		}
	}
}

func TestNewConnectionInvalidEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cfg := config.S3Config{
		Endpoint:      "127.0.0.1:59997",
		AccessKey:     "minioadmin",
		SecretKey:     "minioadmin_secret",
		UseSSL:        false,
		MediaBucket:   "egeinf-media",
		PrivateBucket: "egeinf-private",
	}

	_, err := New(ctx, cfg)
	if err == nil {
		t.Errorf("expected error connecting to non-existent minio endpoint")
	}
}
