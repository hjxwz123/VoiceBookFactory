package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

var bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// OSSConfig connects to an Aliyun OSS bucket.
type OSSConfig struct {
	Region          string // e.g. cn-hangzhou
	Endpoint        string // optional; defaults to oss-<region>.aliyuncs.com
	Internal        bool   // upload and download over the internal network (servers in the same region)
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
	Prefix          string // key prefix inside the bucket, e.g. "voicebook/"
	CustomDomain    string // a domain bound to the bucket (or a CDN in front of it) for browser downloads
}

// OSS stores objects in an Aliyun OSS bucket with the official SDK (V4 signatures).
type OSS struct {
	cfg    OSSConfig
	client *oss.Client // server-side transfers
	signer *oss.Client // URLs handed to browsers
}

func NewOSS(c OSSConfig) (*OSS, error) {
	c.Region = strings.TrimSpace(c.Region)
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.CustomDomain = strings.TrimRight(strings.TrimSpace(c.CustomDomain), "/")
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	if c.Prefix != "" {
		c.Prefix += "/"
	}
	if c.Region == "" || c.Bucket == "" || c.AccessKeyID == "" || c.AccessKeySecret == "" {
		return nil, errors.New("地域、存储桶、AccessKey ID 和 AccessKey Secret 都要填写")
	}
	if !bucketRe.MatchString(c.Bucket) {
		return nil, errors.New("存储桶名称只能包含小写字母、数字和短横线，长度 3~63 个字符，例如 my-audiobooks")
	}
	cred := credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.AccessKeySecret)
	base := func() *oss.Config {
		return oss.LoadDefaultConfig().WithCredentialsProvider(cred).WithRegion(c.Region).
			WithConnectTimeout(10 * time.Second).WithReadWriteTimeout(60 * time.Second)
	}
	cfg := base()
	if c.Endpoint != "" {
		cfg = cfg.WithEndpoint(c.Endpoint)
		if strings.HasPrefix(c.Endpoint, "http://") {
			cfg = cfg.WithDisableSSL(true)
		}
	} else if c.Internal {
		cfg = cfg.WithUseInternalEndpoint(true)
	}
	o := &OSS{cfg: c, client: oss.NewClient(cfg)}
	switch {
	case c.CustomDomain != "":
		sc := base().WithEndpoint(c.CustomDomain).WithUseCName(true)
		if strings.HasPrefix(c.CustomDomain, "http://") {
			sc = sc.WithDisableSSL(true)
		}
		o.signer = oss.NewClient(sc)
	case c.Internal && c.Endpoint == "":
		o.signer = oss.NewClient(base()) // browsers cannot reach the internal endpoint
	default:
		o.signer = o.client
	}
	return o, nil
}

func (o *OSS) Name() string   { return "oss" }
func (o *OSS) Bucket() string { return o.cfg.Bucket }

func (o *OSS) key(k string) *string { return oss.Ptr(o.cfg.Prefix + k) }

func (o *OSS) Upload(ctx context.Context, key, path, contentType string) error {
	_, err := o.client.PutObjectFromFile(ctx, &oss.PutObjectRequest{Bucket: oss.Ptr(o.cfg.Bucket), Key: o.key(key), ContentType: oss.Ptr(contentType)}, path)
	return err
}

func (o *OSS) Download(ctx context.Context, key, path string) error {
	_, err := o.client.GetObjectToFile(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(o.cfg.Bucket), Key: o.key(key)}, path)
	return err
}

func (o *OSS) Stat(ctx context.Context, key string) (int64, error) {
	r, err := o.client.HeadObject(ctx, &oss.HeadObjectRequest{Bucket: oss.Ptr(o.cfg.Bucket), Key: o.key(key)})
	if err != nil {
		return 0, err
	}
	return r.ContentLength, nil
}

func (o *OSS) Delete(ctx context.Context, key string) error {
	_, err := o.client.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: oss.Ptr(o.cfg.Bucket), Key: o.key(key)})
	return err
}

func (o *OSS) SignGet(ctx context.Context, key string, ttl time.Duration, filename string) (string, error) {
	req := &oss.GetObjectRequest{Bucket: oss.Ptr(o.cfg.Bucket), Key: o.key(key)}
	if filename != "" {
		req.ResponseContentDisposition = oss.Ptr("attachment; filename*=UTF-8''" + url.PathEscape(filename))
	}
	r, err := o.signer.Presign(ctx, req, oss.PresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return r.URL, nil
}

// Explain turns an OSS or network error into a sentence an operator can act on.
func (o *OSS) Explain(err error) string {
	if err == nil {
		return ""
	}
	var se *oss.ServiceError
	if errors.As(err, &se) {
		switch {
		case se.Code == "InvalidAccessKeyId":
			return "AccessKey ID 不存在或已停用，请检查是否填错"
		case se.Code == "SignatureDoesNotMatch":
			return "AccessKey Secret 不正确"
		case se.Code == "NoSuchBucket":
			return fmt.Sprintf("存储桶 %s 不存在，请检查名称", o.cfg.Bucket)
		case strings.Contains(se.Message, "specified endpoint") || se.Code == "PermanentRedirect":
			return "存储桶不在这个地域，请改成存储桶所在的地域"
		case se.Code == "AccessDenied" || se.StatusCode == 403:
			return "没有权限：请给这个 AccessKey 授予该存储桶的读写权限（例如 RAM 策略里的 oss:PutObject、oss:GetObject、oss:DeleteObject）。原始信息：" + se.Message
		}
		return fmt.Sprintf("OSS 返回错误 %s：%s", se.Code, se.Message)
	}
	var ne net.Error
	var de *net.DNSError
	if errors.As(err, &de) || errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") {
		if o.cfg.Internal && o.cfg.Endpoint == "" {
			return "连不上 OSS 内网地址：内网上传只能在同地域的阿里云服务器上使用，否则请关掉「内网传输」"
		}
		return "连不上 OSS：请检查地域或 Endpoint 是否正确，以及服务器能否访问外网"
	}
	return err.Error()
}
