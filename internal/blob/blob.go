// Package blob — доступ к S3-совместимому хранилищу блобов (MinIO):
// content-addressed загрузка ruleset-блобов и подписанные URL для агентов.
// Два клиента: внутренний (upload/stat по endpoint из конфига) и публичный
// (подпись URL по public_endpoint, доступному агентам).
package blob

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/surifleet/surifleet/internal/config"
)

// Store — клиент блоб-хранилища для одного бакета.
type Store struct {
	cli    *minio.Client // внутренний (upload/stat)
	signer *minio.Client // подпись URL (public endpoint или = cli)
	bucket string
}

// New создаёт Store и гарантирует существование бакета (dev-контур).
func New(ctx context.Context, cfg config.S3Config) (*Store, error) {
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	cli, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL})
	if err != nil {
		return nil, fmt.Errorf("s3 клиент: %w", err)
	}
	signer := cli
	if cfg.PublicEndpoint != "" && cfg.PublicEndpoint != cfg.Endpoint {
		signer, err = minio.New(cfg.PublicEndpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL})
		if err != nil {
			return nil, fmt.Errorf("s3 клиент подписи: %w", err)
		}
	}
	s := &Store{cli: cli, signer: signer, bucket: cfg.Bucket}

	exists, err := cli.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3 бакет %s: %w", cfg.Bucket, err)
	}
	if !exists {
		if err := cli.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("s3 создание бакета %s: %w", cfg.Bucket, err)
		}
	}
	return s, nil
}

// PutIfAbsent загружает блоб, если ключа ещё нет (content-addressed:
// повторная сборка того же набора — тот же ключ, загрузка пропускается).
// Возвращает uploaded=true, если блоб реально загружен.
func (s *Store) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	_, err := s.cli.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return false, nil // уже есть
	}
	if minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return false, fmt.Errorf("s3 stat %s: %w", key, err)
	}
	_, err = s.cli.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8"})
	if err != nil {
		return false, fmt.Errorf("s3 put %s: %w", key, err)
	}
	return true, nil
}

// PresignGet — подписанный GET URL блоба (для агента), TTL ограничен.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.signer.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("s3 presign %s: %w", key, err)
	}
	return u.String(), nil
}
