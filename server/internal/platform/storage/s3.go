package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Config configures the S3 driver. It works with AWS S3 and with
// compatible stores (MinIO, Ceph, Cloudflare R2, ...).
type S3Config struct {
	Bucket string
	// Region defaults to us-east-1 (most compatible stores ignore it).
	Region string
	// Endpoint is the store's URL for S3-compatible services; empty for AWS.
	Endpoint string
	// AccessKey and SecretKey are static credentials; empty uses the default
	// AWS credential chain (environment, shared config, instance role).
	AccessKey, SecretKey string
	// PathStyle uses path-style addressing (most self-hosted stores need it).
	PathStyle bool
	// Prefix is prepended to every key ("openbekci/").
	Prefix string
}

// S3 is the S3 driver.
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	prefix  string
}

var _ Blob = (*S3)(nil)

// NewS3 returns an S3 store. It does not contact the service.
func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	if c.Bucket == "" {
		return nil, errors.New("storage: S3 bucket is required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(c.Region)}
	if c.AccessKey != "" || c.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("storage: load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
		o.UsePathStyle = c.PathStyle
		// Compatible stores often lack the newer checksum extensions.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &S3{client: client, presign: s3.NewPresignClient(client), bucket: c.Bucket, prefix: c.Prefix}, nil
}

func (s *S3) key(key string) (*string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	return aws.String(s.prefix + key), nil
}

func notFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

// Put implements Blob. The body is spooled (memory for a seekable reader of
// known size, else a temporary file) because signing a request needs its
// length and checksum up front.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	var body io.ReadSeeker
	var length int64
	if rs, ok := r.(io.ReadSeeker); ok && size >= 0 {
		body, length = rs, size
	} else {
		tmp, err := os.CreateTemp("", "openbekci-s3-*")
		if err != nil {
			return fmt.Errorf("storage: spool upload: %w", err)
		}
		defer func() {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}()
		n, err := io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
		if err != nil {
			return fmt.Errorf("storage: spool upload: %w", err)
		}
		if size >= 0 && n != size {
			return fmt.Errorf("storage: read %d bytes, expected %d", n, size)
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("storage: spool upload: %w", err)
		}
		body, length = tmp, n
	}
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: k, Body: body, ContentLength: aws.Int64(length)}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: s3 put: %w", err)
	}
	return nil
}

// Get implements Blob.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	k, err := s.key(key)
	if err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: k})
	if err != nil {
		if notFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: s3 get: %w", err)
	}
	return out.Body, nil
}

// Delete implements Blob.
func (s *S3) Delete(ctx context.Context, key string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: k}); err != nil && !notFound(err) {
		return fmt.Errorf("storage: s3 delete: %w", err)
	}
	return nil
}

// Stat implements Blob.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	k, err := s.key(key)
	if err != nil {
		return Info{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: k})
	if err != nil {
		if notFound(err) {
			return Info{}, ErrNotFound
		}
		return Info{}, fmt.Errorf("storage: s3 head: %w", err)
	}
	return Info{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType)}, nil
}

// DirectDownloads implements DirectDownloader: the presigned URLs are the bucket's own.
func (s *S3) DirectDownloads() bool { return true }

// SignedURL implements Blob: a presigned GET.
func (s *S3) SignedURL(ctx context.Context, key string, ttl time.Duration, opts ...SignOption) (string, error) {
	k, err := s.key(key)
	if err != nil {
		return "", err
	}
	o := ResolveSignOptions(opts)
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: k}
	if o.Disposition != "" {
		in.ResponseContentDisposition = aws.String(o.Disposition)
	}
	if o.ContentType != "" {
		in.ResponseContentType = aws.String(o.ContentType)
	}
	req, err := s.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: s3 presign: %w", err)
	}
	return req.URL, nil
}
