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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const s3TracerName = "gophprofile/storage"

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
	ctx, span := otel.Tracer(s3TracerName).Start(ctx, "s3.ensure_bucket",
		trace.WithAttributes(attribute.String("s3.bucket", s.bucket)),
	)
	defer span.End()

	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

// Upload сохраняет объект в бакет.
func (s *S3) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	ctx, span := otel.Tracer(s3TracerName).Start(ctx, "s3.upload",
		trace.WithAttributes(
			attribute.String("s3.bucket", s.bucket),
			attribute.String("s3.key", key),
			attribute.Int("s3.size_bytes", len(data)),
		),
	)
	defer span.End()

	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// Download читает объект и возвращает содержимое вместе с Content-Type.
func (s *S3) Download(ctx context.Context, key string) ([]byte, string, error) {
	ctx, span := otel.Tracer(s3TracerName).Start(ctx, "s3.download",
		trace.WithAttributes(
			attribute.String("s3.bucket", s.bucket),
			attribute.String("s3.key", key),
		),
	)
	defer span.End()

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, "", err
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, "", err
	}
	data, err := io.ReadAll(obj)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, "", err
	}
	span.SetAttributes(attribute.Int("s3.size_bytes", len(data)))
	return data, stat.ContentType, nil
}

// Delete удаляет объекты; отсутствие ключа не считается ошибкой.
func (s *S3) Delete(ctx context.Context, keys []string) error {
	ctx, span := otel.Tracer(s3TracerName).Start(ctx, "s3.delete",
		trace.WithAttributes(
			attribute.String("s3.bucket", s.bucket),
			attribute.Int("s3.keys_count", len(keys)),
		),
	)
	defer span.End()

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
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
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
