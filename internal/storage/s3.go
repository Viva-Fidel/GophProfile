// Package storage реализует работу с S3-совместимым хранилищем файлов.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectStorage — интерфейс объектного хранилища аватарок.
type ObjectStorage interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) error
	Download(ctx context.Context, key string) ([]byte, string, error)
	Delete(ctx context.Context, keys []string) error
	Ping(ctx context.Context) error
}

// S3 — клиент MinIO/S3.
type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 создаёт клиент S3 без сетевого запроса.
func NewS3(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: client, bucket: bucket}, nil
}

// EnsureBucket создаёт бакет, если его ещё нет.
func (s *S3) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}

// Upload сохраняет объект в бакет.
func (s *S3) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

// Download читает объект и возвращает содержимое вместе с Content-Type.
func (s *S3) Download(ctx context.Context, key string) ([]byte, string, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, "", err
	}
	return data, stat.ContentType, nil
}

// Delete удаляет объекты; отсутствие ключа не считается ошибкой.
func (s *S3) Delete(ctx context.Context, keys []string) error {
	for _, key := range keys {
		if key == "" {
			continue
		}
		err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
		if err != nil {
			errResp := minio.ToErrorResponse(err)
			if errResp.StatusCode == http.StatusNotFound || errResp.Code == "NoSuchKey" {
				continue
			}
			return fmt.Errorf("delete %s: %w", key, err)
		}
	}
	return nil
}

// Ping проверяет доступность бакета.
func (s *S3) Ping(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}
