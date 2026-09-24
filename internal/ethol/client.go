package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type browserProfile struct {
	userAgent       string
	secChUa         string
	secChUaMobile   string
	secChUaPlatform string
}

// ponytail: static legit desktop browser profiles. upgrade path: load from env or remote feed if ETHOL bans signatures.
var browserProfiles = []browserProfile{
	{
		userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"Windows"`,
	},
	{
		userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"macOS"`,
	},
	{
		userAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"Linux"`,
	},
	{
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	{
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	{
		userAgent: "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	{
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
	},
	{
		userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
		secChUa:         `"Microsoft Edge";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"Windows"`,
	},
	{
		userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
		secChUa:         `"Microsoft Edge";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"macOS"`,
	},
}

type headerTransport struct {
	base    http.RoundTripper
	profile browserProfile
}

const (
	maxTransportRetries = 2
	transportRetryBase  = 150 * time.Millisecond
)

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	setDefaultHeader(req.Header, "User-Agent", t.profile.userAgent)

	if isTargetHost(req.URL.Hostname()) {
		setDefaultHeader(req.Header, "Accept", "application/json, text/plain, */*")
		setDefaultHeader(req.Header, "Accept-Language", "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7")
		setDefaultHeader(req.Header, "Sec-Fetch-Site", "same-origin")
		setDefaultHeader(req.Header, "Sec-Fetch-Mode", "cors")
		setDefaultHeader(req.Header, "Sec-Fetch-Dest", "empty")
		if t.profile.secChUa != "" {
			setDefaultHeader(req.Header, "Sec-CH-UA", t.profile.secChUa)
			setDefaultHeader(req.Header, "Sec-CH-UA-Mobile", t.profile.secChUaMobile)
			setDefaultHeader(req.Header, "Sec-CH-UA-Platform", t.profile.secChUaPlatform)
		}
	}

	for attempt := 0; ; attempt++ {
		start := time.Now()
		resp, err := t.base.RoundTrip(req)
		dur := time.Since(start)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		if isDevBuild {
			devLogHTTP(req.Method, req.URL.String(), status, dur, err)
		}
		if attempt >= maxTransportRetries || !retryableRequest(req) || !retryableResponse(resp, err) {
			return resp, err
		}
		if resp != nil {
			if resp.Body != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
			}
		}
		if err := waitTransportRetry(req.Context(), resp, attempt); err != nil {
			return nil, err
		}
	}
}

func retryableRequest(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func retryableResponse(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	return resp != nil && (resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout)
}

func waitTransportRetry(ctx context.Context, resp *http.Response, attempt int) error {
	delay := transportRetryBase * time.Duration(1<<attempt)
	if resp != nil {
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 10 {
			delay = time.Duration(seconds) * time.Second
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func setDefaultHeader(h http.Header, key, value string) {
	if value != "" && h.Get(key) == "" {
		h.Set(key, value)
	}
}

func isTargetHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "pens.ac.id" || strings.HasSuffix(host, ".pens.ac.id")
}

func randomBrowserProfile() browserProfile {
	return browserProfiles[rand.IntN(len(browserProfiles))]
}

type syncCookieJar struct {
	mu  sync.RWMutex
	jar http.CookieJar
}

func newSyncCookieJar() (*syncCookieJar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &syncCookieJar{jar: inner}, nil
}

func (s *syncCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jar.SetCookies(u, cookies)
}

func (s *syncCookieJar) Cookies(u *url.URL) []*http.Cookie {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jar.Cookies(u)
}

func (s *syncCookieJar) Reset() error {
	newInner, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.jar = newInner
	s.mu.Unlock()
	return nil
}

func NewHTTPClient() (*http.Client, error) {
	jar, err := newSyncCookieJar()
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 6,
		IdleConnTimeout:     45 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &http.Client{
		Jar:       jar,
		Transport: &headerTransport{base: transport, profile: randomBrowserProfile()},
		Timeout:   30 * time.Second,
	}, nil
}

type cookieSession struct {
	Cookies map[string][]*http.Cookie `json:"cookies"`
}

// SaveCookies persists the cookies matching urls to path using an atomic write.
// It is used to reuse the CAS session across restarts.
func SaveCookies(jar http.CookieJar, path string, urls []string) error {
	if jar == nil || path == "" {
		return nil
	}
	session := cookieSession{Cookies: make(map[string][]*http.Cookie)}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if cookies := jar.Cookies(u); len(cookies) > 0 {
			session.Cookies[raw] = cookies
		}
	}
	if len(session.Cookies) == 0 {
		return nil
	}
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal cookie session: %w", err)
	}
	return writeFileAtomic(path, data, 0600)
}

// LoadCookies restores cookies previously written by SaveCookies.
func LoadCookies(jar http.CookieJar, path string, urls []string) error {
	if jar == nil || path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var session cookieSession
	if err := json.Unmarshal(data, &session); err != nil {
		return fmt.Errorf("parse cookie session: %w", err)
	}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if cookies := session.Cookies[raw]; len(cookies) > 0 {
			jar.SetCookies(u, cookies)
		}
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return fmt.Errorf("create temp session file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
