package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"backend/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Storage wraps the MinIO client with application-level storage methods.
type Storage struct {
	client        *minio.Client
	mediaBucket   string
	privateBucket string
}

// New initializes the S3/MinIO client and ensures required buckets exist with appropriate policies.
func New(ctx context.Context, cfg config.S3Config) (*Storage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: client init error: %w", err)
	}

	s := &Storage{
		client:        client,
		mediaBucket:   cfg.MediaBucket,
		privateBucket: cfg.PrivateBucket,
	}

	if err := s.EnsureBuckets(ctx); err != nil {
		return nil, fmt.Errorf("s3: bucket bootstrap error: %w", err)
	}

	return s, nil
}

// Wrap wraps an existing *minio.Client for testing and dependency injection.
func Wrap(client *minio.Client, mediaBucket, privateBucket string) *Storage {
	return &Storage{
		client:        client,
		mediaBucket:   mediaBucket,
		privateBucket: privateBucket,
	}
}

// Underlying returns the underlying *minio.Client.
func (s *Storage) Underlying() *minio.Client {
	return s.client
}

// MediaBucket returns the public media bucket name.
func (s *Storage) MediaBucket() string {
	return s.mediaBucket
}

// PrivateBucket returns the restricted private bucket name.
func (s *Storage) PrivateBucket() string {
	return s.privateBucket
}

// PublicURL returns the single-origin proxied URL for public media assets (/storage/...).
func (s *Storage) PublicURL(objectKey string) string {
	return "/storage/" + strings.TrimPrefix(objectKey, "/")
}

// EnsureBuckets ensures both media (public read) and private (restricted) buckets exist.
func (s *Storage) EnsureBuckets(ctx context.Context) error {
	// 1. Media bucket (public read policy)
	if err := s.ensureBucket(ctx, s.mediaBucket); err != nil {
		return fmt.Errorf("ensure media bucket '%s': %w", s.mediaBucket, err)
	}
	publicPolicy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {"AWS": ["*"]},
				"Action": ["s3:GetBucketLocation", "s3:ListBucket"],
				"Resource": ["arn:aws:s3:::%s"]
			},
			{
				"Effect": "Allow",
				"Principal": {"AWS": ["*"]},
				"Action": ["s3:GetObject"],
				"Resource": ["arn:aws:s3:::%s/*"]
			}
		]
	}`, s.mediaBucket, s.mediaBucket)
	if err := s.client.SetBucketPolicy(ctx, s.mediaBucket, publicPolicy); err != nil {
		return fmt.Errorf("set public policy for '%s': %w", s.mediaBucket, err)
	}

	// 2. Private bucket (restricted, no public policy)
	if err := s.ensureBucket(ctx, s.privateBucket); err != nil {
		return fmt.Errorf("ensure private bucket '%s': %w", s.privateBucket, err)
	}

	return nil
}

func (s *Storage) ensureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		err = s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// Upload streams data to an S3 object with a specified content type.
func (s *Storage) Upload(ctx context.Context, bucket, objectKey string, reader io.Reader, size int64, contentType string) (string, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	opts := minio.PutObjectOptions{
		ContentType: contentType,
	}

	info, err := s.client.PutObject(ctx, bucket, objectKey, reader, size, opts)
	if err != nil {
		return "", fmt.Errorf("s3: upload failed for '%s/%s': %w", bucket, objectKey, err)
	}

	return info.Key, nil
}

// UploadBytes uploads an in-memory byte slice as an S3 object.
func (s *Storage) UploadBytes(ctx context.Context, bucket, objectKey string, data []byte, contentType string) (string, error) {
	return s.Upload(ctx, bucket, objectKey, bytes.NewReader(data), int64(len(data)), contentType)
}

// Download returns a readable object stream. Caller is responsible for closing the reader.
func (s *Storage) Download(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3: download failed for '%s/%s': %w", bucket, objectKey, err)
	}
	// Verify object exists by checking stat
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("s3: object stat failed for '%s/%s': %w", bucket, objectKey, err)
	}
	return obj, nil
}

// DownloadBytes downloads an entire S3 object into memory.
func (s *Storage) DownloadBytes(ctx context.Context, bucket, objectKey string) ([]byte, error) {
	reader, err := s.Download(ctx, bucket, objectKey)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

// Delete removes an object from a bucket.
func (s *Storage) Delete(ctx context.Context, bucket, objectKey string) error {
	err := s.client.RemoveObject(ctx, bucket, objectKey, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3: delete failed for '%s/%s': %w", bucket, objectKey, err)
	}
	return nil
}

// Stat retrieves metadata about an object.
func (s *Storage) Stat(ctx context.Context, bucket, objectKey string) (minio.ObjectInfo, error) {
	return s.client.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{})
}

// GetPresignedURL generates a time-limited presigned GET URL for an object.
func (s *Storage) GetPresignedURL(ctx context.Context, bucket, objectKey string, expires time.Duration) (string, error) {
	reqParams := make(url.Values)
	u, err := s.client.PresignedGetObject(ctx, bucket, objectKey, expires, reqParams)
	if err != nil {
		return "", fmt.Errorf("s3: presigned url failed for '%s/%s': %w", bucket, objectKey, err)
	}
	return u.String(), nil
}
