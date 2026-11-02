// Package s3mini is a tiny S3 client (SigV4, path-style) with just what control backups need:
// put a file, list a prefix, download and delete an object. It talks to the same bucket the
// service backups use (AWS S3, Cloudflare R2, Backblaze B2, MinIO, ...).
package s3mini

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Client is one bucket on one endpoint.
type Client struct {
	Endpoint  string // https://host[:port] or host (https assumed)
	Bucket    string
	Region    string // "" = us-east-1 (R2 accepts it as an alias of auto)
	AccessKey string
	SecretKey string
	HTTP      *http.Client
	Now       func() time.Time
}

// Object is one listed object.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

func (c *Client) base() (*url.URL, error) {
	ep := strings.TrimRight(c.Endpoint, "/")
	if !strings.Contains(ep, "://") {
		ep = "https://" + ep
	}
	return url.Parse(ep)
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

func (c *Client) region() string {
	if c.Region == "" {
		return "us-east-1"
	}
	return c.Region
}

// escapePath URI-encodes each segment of an object key (SigV4 canonical form).
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
	}
	return strings.Join(segs, "/")
}

func (c *Client) request(ctx context.Context, method, key string, q url.Values, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	u, err := c.base()
	if err != nil {
		return nil, err
	}
	path := "/" + c.Bucket
	if key != "" {
		path += "/" + strings.TrimLeft(key, "/")
	}
	raw := escapePath(path)
	u.Path, u.RawPath = path, raw
	u.RawQuery = canonicalQuery(q)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	if payloadHash == "" {
		payloadHash = emptyHash
	}
	c.sign(req, raw, payloadHash)
	return c.hc().Do(req)
}

const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func awsEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(s), "+", "%20"), "%7E", "~")
}

func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

// sign adds SigV4 headers (host, x-amz-date, x-amz-content-sha256, authorization).
func (c *Client) sign(req *http.Request, canonicalPath, payloadHash string) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now().UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	host := req.URL.Host
	signed := "host;x-amz-content-sha256;x-amz-date"
	canon := strings.Join([]string{
		req.Method, canonicalPath, req.URL.RawQuery,
		"host:" + host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + amzDate + "\n",
		signed, payloadHash,
	}, "\n")
	scope := day + "/" + c.region() + "/s3/aws4_request"
	ch := sha256.Sum256([]byte(canon))
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	k := hmacSHA([]byte("AWS4"+c.SecretKey), day)
	k = hmacSHA(k, c.region())
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, sts))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func respErr(op string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(b, &e) == nil && e.Code != "" {
		return fmt.Errorf("s3 %s: HTTP %d %s: %s", op, resp.StatusCode, e.Code, e.Message)
	}
	return fmt.Errorf("s3 %s: HTTP %d", op, resp.StatusCode)
}

// PutFile uploads the file at path as key.
func (c *Client) PutFile(ctx context.Context, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	resp, err := c.request(ctx, http.MethodPut, key, nil, f, n, hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return respErr("put "+key, resp)
	}
	return nil
}

// Get downloads key; the caller closes the body.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	resp, err := c.request(ctx, http.MethodGet, key, nil, nil, 0, "")
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, 0, respErr("get "+key, resp)
	}
	return resp.Body, resp.ContentLength, nil
}

// Delete removes key (missing keys are not an error).
func (c *Client) Delete(ctx context.Context, key string) error {
	resp, err := c.request(ctx, http.MethodDelete, key, nil, nil, 0, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != 404 {
		return respErr("delete "+key, resp)
	}
	return nil
}

// List returns every object under prefix (ListObjectsV2, paginated).
func (c *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	out := []Object{}
	token := ""
	for range 100 {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := c.request(ctx, http.MethodGet, "", q, nil, 0, "")
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			err := respErr("list "+prefix, resp)
			resp.Body.Close()
			return nil, err
		}
		var lr struct {
			Contents []struct {
				Key          string `xml:"Key"`
				Size         int64  `xml:"Size"`
				LastModified string `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&lr)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("s3 list: %w", err)
		}
		for _, o := range lr.Contents {
			lm, _ := time.Parse(time.RFC3339, o.LastModified)
			out = append(out, Object{Key: o.Key, Size: o.Size, LastModified: lm})
		}
		if !lr.IsTruncated || lr.NextContinuationToken == "" {
			break
		}
		token = lr.NextContinuationToken
	}
	return out, nil
}
