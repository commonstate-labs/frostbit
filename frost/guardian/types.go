                                                                               
                                                                             
package guardian

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Request struct {
	Chain        string `json:"chain"`
	VaultID      uint32 `json:"vaultId"`
	Epoch        uint64 `json:"epoch"`
	JobID        string `json:"jobId"`
	TemplateHash []byte `json:"templateHash"`
}

type Intent struct {
	Request   Request  `json:"request"`
	SignAlgo  int32    `json:"signAlgo"`
	PublicKey []byte   `json:"publicKey"`
	Messages  [][]byte `json:"messages"`
}

type Response struct {
	Signatures [][]byte `json:"signatures"`
}
type Resolver interface {
	Resolve(context.Context, Request) (*Intent, error)
}
type ResolverFunc func(context.Context, Request) (*Intent, error)

func (f ResolverFunc) Resolve(c context.Context, r Request) (*Intent, error) { return f(c, r) }

type Client interface {
	Sign(context.Context, Request) ([][]byte, error)
}

type HTTPClient struct {
	URL    string
	Client *http.Client
}

func NewHTTPClient(url string) *HTTPClient {
	return &HTTPClient{URL: strings.TrimRight(url, "/"), Client: &http.Client{Timeout: 30 * time.Second}}
}
func (c *HTTPClient) Sign(ctx context.Context, r Request) ([][]byte, error) {
	var response Response
	if err := c.post(ctx, "/sign", r, &response); err != nil {
		return nil, err
	}
	return response.Signatures, nil
}
func (c *HTTPClient) Resolve(ctx context.Context, r Request) (*Intent, error) {
	var result Intent
	if err := c.post(ctx, "/frost/guardian/intent", r, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (c *HTTPClient) post(ctx context.Context, path string, in, out any) error {
	if c == nil || c.Client == nil || c.URL == "" {
		return fmt.Errorf("guardian endpoint is not configured")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("guardian HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(out)
}

func DecodeRequest(w http.ResponseWriter, r *http.Request) (Request, error) {
	var request Request
	if r.Method != http.MethodPost {
		return request, fmt.Errorf("POST required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&request); err != nil {
		return request, err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return request, fmt.Errorf("trailing request data")
	}
	if request.JobID == "" || len(request.JobID) > 256 || request.Chain == "" || request.Epoch == 0 || len(request.TemplateHash) != 32 {
		return request, fmt.Errorf("complete finalized intent identity required")
	}
	return request, nil
}
