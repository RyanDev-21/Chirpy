package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Storage holds the files. Phones upload and download directly with
// short-lived signed links; the server only checks and deletes.
type Storage interface {
	// PresignPut is a PUT link that only accepts exactly this type and size,
	// once: it can't overwrite a file that is already there.
	PresignPut(key, contentType string, size int64, ttl time.Duration) (string, error)
	// PresignGet is a download link that makes browsers save the file
	// (Content-Disposition: attachment, with fileName if set) with the given
	// type, never display or sniff it.
	PresignGet(key, contentType, fileName string, ttl time.Duration) (string, error)
	// Size is the stored object's size; ErrNoObject if it isn't there.
	Size(ctx context.Context, key string) (int64, error)
	Delete(ctx context.Context, key string) error
	// Put stores a small object the server made itself (thumbnails).
	Put(ctx context.Context, key, contentType string, data []byte) error
	// Copy duplicates an object inside the bucket (forwarding), without
	// downloading it; ErrNoObject if src is gone.
	Copy(ctx context.Context, src, dst string) error
}

var ErrNoObject = errors.New("object not found")

// R2 is Cloudflare R2 through its S3-compatible API, signed with AWS
// Signature Version 4 (no SDK needed for four operations).
type R2 struct {
	host, bucket string
	signer       sigV4
	client       *http.Client
}

func NewR2(accountID, bucket, accessKey, secretKey string) *R2 {
	return &R2{
		host:   accountID + ".r2.cloudflarestorage.com",
		bucket: bucket,
		signer: sigV4{accessKey: accessKey, secretKey: secretKey, region: "auto", service: "s3"},
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (r *R2) path(key string) string { return "/" + r.bucket + "/" + key }

func (r *R2) PresignPut(key, contentType string, size int64, ttl time.Duration) (string, error) {
	return r.signer.presign(http.MethodPut, r.host, r.path(key), map[string]string{
		"content-length": strconv.FormatInt(size, 10),
		"content-type":   contentType,
		"if-none-match":  "*", // write once: no swapping the file after it was sent
	}, nil, ttl, time.Now()), nil
}

func (r *R2) PresignGet(key, contentType, fileName string, ttl time.Duration) (string, error) {
	return r.signer.presign(http.MethodGet, r.host, r.path(key), nil, map[string]string{
		"response-content-disposition": disposition(fileName),
		"response-content-type":        contentType,
	}, ttl, time.Now()), nil
}

func (r *R2) do(ctx context.Context, method, key string) (*http.Response, error) {
	u := r.signer.presign(method, r.host, r.path(key), nil, nil, time.Minute, time.Now())
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	return r.client.Do(req)
}

func (r *R2) Size(ctx context.Context, key string) (int64, error) {
	res, err := r.do(ctx, http.MethodHead, key)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return res.ContentLength, nil
	case http.StatusNotFound:
		return 0, ErrNoObject
	}
	return 0, fmt.Errorf("r2 head: %s", res.Status)
}

func (r *R2) Delete(ctx context.Context, key string) error {
	res, err := r.do(ctx, http.MethodDelete, key)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK || res.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("r2 delete: %s", res.Status)
}

// sigV4 makes query-string-signed ("presigned") S3 URLs.
type sigV4 struct {
	accessKey, secretKey, region, service string
}

// uriEncode is S3's encoding: everything but A-Z a-z 0-9 - _ . ~ is
// percent-encoded; "/" is kept in paths.
func uriEncode(s string, keepSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// presign signs method+path with the given headers (which the client must
// then send exactly), valid for ttl from now.
func (s sigV4) presign(method, host, path string, headers, params map[string]string, ttl time.Duration, now time.Time) string {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	scope := day + "/" + s.region + "/" + s.service + "/aws4_request"

	hdrs := map[string]string{"host": host}
	for k, v := range headers {
		hdrs[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	names := make([]string, 0, len(hdrs))
	for k := range hdrs {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + hdrs[k] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", s.accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	for k, v := range params {
		q.Set(k, v)
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = uriEncode(k, false) + "=" + uriEncode(q.Get(k), false)
	}
	canonQuery := strings.Join(parts, "&")
	canonPath := uriEncode(path, true)

	canonical := strings.Join([]string{method, canonPath, canonQuery, canonHeaders.String(), signedHeaders, "UNSIGNED-PAYLOAD"}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])

	key := hmacSHA256([]byte("AWS4"+s.secretKey), day)
	key = hmacSHA256(key, s.region)
	key = hmacSHA256(key, s.service)
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	return "https://" + host + canonPath + "?" + canonQuery + "&X-Amz-Signature=" + sig
}

func (r *R2) Put(ctx context.Context, key, contentType string, data []byte) error {
	u := r.signer.presign(http.MethodPut, r.host, r.path(key), map[string]string{
		"content-length": strconv.Itoa(len(data)),
		"content-type":   contentType,
	}, nil, time.Minute, time.Now())
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(data))
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("r2 put: %s", res.Status)
	}
	return nil
}

// disposition is "attachment", with the file name in RFC 6266 / 5987 form
// (UTF-8, percent-encoded) when there is one.
func disposition(fileName string) string {
	if fileName == "" {
		return "attachment"
	}
	return "attachment; filename*=UTF-8''" + url.PathEscape(fileName)
}

// Copy is S3 CopyObject: R2 copies the bytes itself (no egress).
func (r *R2) Copy(ctx context.Context, src, dst string) error {
	source := "/" + r.bucket + "/" + src
	u := r.signer.presign(http.MethodPut, r.host, r.path(dst), map[string]string{
		"x-amz-copy-source": source,
	}, nil, time.Minute, time.Now())
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-amz-copy-source", source)
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return ErrNoObject
	case res.StatusCode != http.StatusOK || bytes.Contains(body, []byte("<Error>")):
		return fmt.Errorf("r2 copy: %s", res.Status)
	}
	return nil
}
