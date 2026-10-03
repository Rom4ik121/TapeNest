// Package storage is the MinIO layer for original photos and exports.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 talks to MinIO.
type S3 struct {
	client *minio.Client
	public *minio.Client
	region string
}

// Options for New.
type Options struct {
	Endpoint, AccessKey, SecretKey, Region, PublicURL string
	UseSSL                                            bool
}

// New builds the clients.
func New(o Options) (*S3, error) {
	creds := credentials.NewStaticV4(o.AccessKey, o.SecretKey, "")
	c, err := minio.New(o.Endpoint, &minio.Options{Creds: creds, Secure: o.UseSSL, Region: o.Region})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	s := &S3{client: c, region: o.Region}
	if o.PublicURL != "" {
		u, err := url.Parse(o.PublicURL)
		if err != nil || u.Host == "" {
			return nil, errors.New("s3 public url: bad url")
		}
		pc, err := minio.New(u.Host, &minio.Options{Creds: creds, Secure: u.Scheme == "https", Region: o.Region})
		if err != nil {
			return nil, fmt.Errorf("s3 public client: %w", err)
		}
		s.public = pc
	}
	return s, nil
}

// EnsureBucket creates a private bucket.
func (s *S3) EnsureBucket(ctx context.Context, bucket string) error {
	ok, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !ok {
		return s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.region})
	}
	return nil
}

// Ping checks the bucket.
func (s *S3) Ping(ctx context.Context, bucket string) error {
	_, err := s.client.BucketExists(ctx, bucket)
	return err
}

// Put uploads r.
func (s *S3) Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get reads an object, refusing anything past max.
func (s *S3) Get(ctx context.Context, bucket, key string, max int64) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()
	info, err := obj.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size > max {
		return nil, fmt.Errorf("object is %d bytes", info.Size)
	}
	return io.ReadAll(io.LimitReader(obj, max+1))
}

// Presign returns a public GET URL, or "" when no public host is set.
func (s *S3) Presign(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	if s.public == nil {
		return "", nil
	}
	u, err := s.public.PresignedGetObject(ctx, bucket, key, ttl, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
