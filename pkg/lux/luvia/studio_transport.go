package luvia

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// studioURLFromEnv validates credential transport once, outside request paths.
func studioURLFromEnv() (string, error) {
	value := os.Getenv("LUXO_STUDIO_URL")
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("LUXO_STUDIO_URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	allowHTTP := false
	if flag := os.Getenv("LUXO_STUDIO_ALLOW_INSECURE_HTTP"); flag != "" {
		allowHTTP, err = strconv.ParseBool(flag)
		if err != nil {
			return "", fmt.Errorf("LUXO_STUDIO_ALLOW_INSECURE_HTTP must be a boolean")
		}
	}
	host := u.Hostname()
	loopback := strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
	if u.Scheme == "http" && !loopback && !allowHTTP {
		return "", fmt.Errorf("remote LUXO_STUDIO_URL requires HTTPS; explicitly set LUXO_STUDIO_ALLOW_INSECURE_HTTP=true only on a trusted network")
	}
	return strings.TrimRight(value, "/"), nil
}

// Never redirect telemetry: credentials can also be present in the JSON body.
func newStudioHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}
