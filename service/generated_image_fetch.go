package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const defaultGeneratedImageFetchLimit int64 = 10 * 1024 * 1024

var (
	generatedImageFetchClientOnce sync.Once
	generatedImageFetchClient     *http.Client
)

func generatedImageProtection() (*common.SSRFProtection, bool, error) {
	return &common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		DomainList:             nil,
		IpFilterMode:           false,
		IpList:                 nil,
		AllowedPorts:           []int{443},
		ApplyIPFilterForDomain: true,
	}, true, nil
}

func getGeneratedImageFetchClient() *http.Client {
	generatedImageFetchClientOnce.Do(func() {
		client := newProtectedFetchHTTPClientWithProxy(
			nil,
			nil,
			generatedImageProtection,
			func(*http.Request) (*url.URL, error) { return nil, nil },
		)
		client.Timeout = 15 * time.Second
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("stopped after 3 redirects")
			}
			if req == nil || req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("redirect blocked: generated image URL must use HTTPS")
			}
			if err := validateURLWithProtectionGetter(req.URL.String(), generatedImageProtection); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		}
		generatedImageFetchClient = client
	})
	return generatedImageFetchClient
}

// FetchPublicGeneratedImage downloads a short-lived upstream image URL and
// returns OpenAI-compatible base64. It deliberately uses a stricter policy
// than the configurable generic fetch client: HTTPS only, public IPs only,
// port 443 only, no environment proxy, bounded redirects/time/body size, and
// passive raster image MIME types only.
func FetchPublicGeneratedImage(ctx context.Context, rawURL string, maxBytes int64) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid generated image URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("generated image URL must use HTTPS")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("generated image URL must not contain credentials")
	}
	if err := validateURLWithProtectionGetter(parsed.String(), generatedImageProtection); err != nil {
		return "", fmt.Errorf("generated image URL is not permitted: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = defaultGeneratedImageFetchLimit
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create generated image request: %w", err)
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif")
	resp, err := getGeneratedImageFetchClient().Do(req)
	if err != nil {
		// net/http includes the full request URL in *url.Error.Error(). Generated
		// image URLs commonly contain short-lived bearer material in their path
		// or query, so unwrap it before this error reaches application logs.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			err = urlErr.Err
		}
		return "", fmt.Errorf("download generated image: %w", err)
	}
	defer CloseResponseBodyGracefully(resp)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("generated image endpoint returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return "", fmt.Errorf("generated image exceeds the %d byte limit", maxBytes)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read generated image: %w", err)
	}
	if int64(len(payload)) > maxBytes {
		return "", fmt.Errorf("generated image exceeds the %d byte limit", maxBytes)
	}
	if len(payload) == 0 {
		return "", fmt.Errorf("generated image response is empty")
	}

	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = strings.ToLower(http.DetectContentType(payload))
	}
	switch mimeType {
	case "image/avif", "image/webp", "image/png", "image/jpeg", "image/gif":
	default:
		return "", fmt.Errorf("generated image endpoint returned unsupported content type %q", mimeType)
	}

	return base64.StdEncoding.EncodeToString(payload), nil
}
