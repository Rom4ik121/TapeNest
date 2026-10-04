// Package storage is the MinIO layer for source reads and edit writes.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
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
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
	PublicURL string
}

// New builds the clients. Nothing is contacted until the first call.
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
		return fmt.Errorf("bucket exists: %w", err)
	}
	if !ok {
		if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}
	return nil
}

// Ping checks the media bucket.
func (s *S3) Ping(ctx context.Context, bucket string) error {
	if _, err := s.client.BucketExists(ctx, bucket); err != nil {
		return fmt.Errorf("s3 ping: %w", err)
	}
	return nil
}

// Download copies an object to path, refusing anything past max.
func (s *S3) Download(ctx context.Context, bucket, key, path string, max int64) error {
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3 get: %w", err)
	}
	defer func() { _ = obj.Close() }()
	info, err := obj.Stat()
	if err != nil {
		return fmt.Errorf("s3 stat: %w", err)
	}
	if max > 0 && info.Size > max {
		return fmt.Errorf("object is %d bytes", info.Size)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, io.LimitReader(obj, max+1)); err != nil {
		return fmt.Errorf("s3 copy: %w", err)
	}
	return nil
}

// Put uploads r.
func (s *S3) Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType, fileName string) error {
	_, err := s.client.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType, ContentDisposition: disposition(fileName),
	})
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	return nil
}

// Presign returns a public GET URL, or "" when no public host is configured.
func (s *S3) Presign(ctx context.Context, bucket, key, fileName string, ttl time.Duration) (string, error) {
	if s.public == nil {
		return "", nil
	}
	q := url.Values{}
	if fileName != "" {
		q.Set("response-content-disposition", disposition(fileName))
	}
	u, err := s.public.PresignedGetObject(ctx, bucket, key, ttl, q)
	if err != nil {
		return "", fmt.Errorf("s3 presign: %w", err)
	}
	return u.String(), nil
}

func disposition(name string) string {
	if name == "" {
		return ""
	}
	b := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			r = '_'
		}
		b = append(b, r)
	}
	ascii := string(b)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}
