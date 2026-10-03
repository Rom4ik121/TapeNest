// Package storage is the S3 (MinIO) layer of download-service (spec §4.1:
// media-storage lives here as a package until an editor service appears).
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
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// Config for the S3 client.
type Config struct {
	Endpoint  string // host:port of MinIO as seen from the service
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
	Bucket    string
	// PublicURL is the public origin whose /<bucket>/ path is routed to MinIO
	// (nginx in prod, the Vite proxy in dev). Links for users are presigned
	// against it; empty = no public links.
	PublicURL          string
	Retention          time.Duration
	MultipartThreshold int64
}

// S3 stores downloaded files.
type S3 struct {
	cfg    Config
	client *minio.Client
	public *minio.Client // same credentials, public host; used only for presigning
}

// New creates the client. Nothing is contacted until the first call.
func New(cfg Config) (*S3, error) {
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	c, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL, Region: cfg.Region})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	s := &S3{cfg: cfg, client: c}
	if cfg.PublicURL != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || u.Host == "" {
			return nil, errors.New("s3 public url: bad url")
		}
		// Region is fixed, so presigning never calls the (unreachable) public host.
		pc, err := minio.New(u.Host, &minio.Options{Creds: creds, Secure: u.Scheme == "https", Region: cfg.Region})
		if err != nil {
			return nil, fmt.Errorf("s3 public client: %w", err)
		}
		s.public = pc
	}
	return s, nil
}

// Bucket returns the media bucket name.
func (s *S3) Bucket() string { return s.cfg.Bucket }

// EnsureBucket creates the bucket (private) and its expiry lifecycle rule.
func (s *S3) EnsureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.cfg.Bucket)
	if err != nil {
		return fmt.Errorf("bucket exists: %w", err)
	}
	if !ok {
		if err := s.client.MakeBucket(ctx, s.cfg.Bucket, minio.MakeBucketOptions{Region: s.cfg.Region}); err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}
	if days := int(s.cfg.Retention.Hours() / 24); days > 0 {
		lc := lifecycle.NewConfiguration()
		lc.Rules = []lifecycle.Rule{{
			ID: "expire-downloads", Status: "Enabled",
			Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(days)},
		}}
		if err := s.client.SetBucketLifecycle(ctx, s.cfg.Bucket, lc); err != nil {
			return fmt.Errorf("bucket lifecycle: %w", err)
		}
	}
	return nil
}

// Ping checks connectivity (readyz).
func (s *S3) Ping(ctx context.Context) error {
	if _, err := s.client.BucketExists(ctx, s.cfg.Bucket); err != nil {
		return fmt.Errorf("s3 ping: %w", err)
	}
	return nil
}

// Put uploads r. size < 0 means unknown (stdout pipe): streamed in 16 MiB
// multipart chunks without a temp file. Known sizes below the threshold go
// in one PUT; larger ones use multipart (spec §5.3: multipart > 100 MB).
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType, fileName string) (int64, error) {
	opts := minio.PutObjectOptions{
		ContentType:        contentType,
		ContentDisposition: disposition(fileName),
		PartSize:           16 << 20,
	}
	if size >= 0 && size < s.cfg.MultipartThreshold {
		opts.DisableMultipart = true
		opts.PartSize = 0
	}
	info, err := s.client.PutObject(ctx, s.cfg.Bucket, key, r, size, opts)
	if err != nil {
		return 0, fmt.Errorf("s3 put: %w", err)
	}
	return info.Size, nil
}

// Exists reports whether an object is still there (dedup safety net).
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.cfg.Bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false, nil
	}
	return false, fmt.Errorf("s3 stat: %w", err)
}

// Remove deletes an object (tests, cleanup).
func (s *S3) Remove(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{})
}

// PresignInternal returns a GET URL on the internal endpoint (service-to-service).
func (s *S3) PresignInternal(ctx context.Context, key, fileName string, ttl time.Duration) (string, error) {
	return presign(ctx, s.client, s.cfg.Bucket, key, fileName, ttl)
}

// PresignInline returns a user-facing GET URL without an attachment disposition,
// so a browser can show the object in an <img> or <video>.
func (s *S3) PresignInline(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s.public == nil {
		return "", nil
	}
	u, err := s.public.PresignedGetObject(ctx, s.cfg.Bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("s3 presign: %w", err)
	}
	return u.String(), nil
}

// Open reads an object. The caller closes the body.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get: %w", err)
	}
	return obj, nil
}

// PresignPublic returns a user-facing GET URL through the public origin ("" when
// no PublicURL is configured).
func (s *S3) PresignPublic(ctx context.Context, key, fileName string, ttl time.Duration) (string, error) {
	if s.public == nil {
		return "", nil
	}
	return presign(ctx, s.public, s.cfg.Bucket, key, fileName, ttl)
}

func presign(ctx context.Context, c *minio.Client, bucket, key, fileName string, ttl time.Duration) (string, error) {
	q := url.Values{}
	if fileName != "" {
		q.Set("response-content-disposition", disposition(fileName))
	}
	u, err := c.PresignedGetObject(ctx, bucket, key, ttl, q)
	if err != nil {
		return "", fmt.Errorf("s3 presign: %w", err)
	}
	return u.String(), nil
}

// disposition builds an RFC 6266 attachment header with a UTF-8 file name.
func disposition(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiName(name), url.PathEscape(name))
}

func asciiName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}
