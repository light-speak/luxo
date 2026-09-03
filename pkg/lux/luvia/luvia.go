package luvia

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/env"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Gateway is the Luvia API gateway.
type Gateway struct {
	Router    *api.Router
	modules   []string
	server    *http.Server
	metrics   *MetricsCollector
	registrar *GatewayRegistrar
}

// New creates a new Luvia gateway.
func New() *Gateway {
	router := api.NewRouter()
	router.IdentityExtractor = func(ctx context.Context) any {
		return Identity(ctx)
	}
	router.InternalRequestContext = internalRequestContext
	return &Gateway{
		Router: router,
	}
}

func internalRequestContext(ctx context.Context, bearerToken string) (context.Context, error) {
	cfg, err := auth.LoadConfig()
	if err != nil {
		return nil, err
	}
	data, err := auth.VerifyCached(cfg, bearerToken)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, identityKey, &Claims{data: data})
	return context.WithValue(ctx, bearerTokenCtxKey{}, bearerToken), nil
}

// AddModule registers a module name for display.
func (g *Gateway) AddModule(name string) {
	g.modules = append(g.modules, name)
}

// Serve starts the HTTP/2 server with graceful shutdown support.
// Default: h2c (HTTP/2 cleartext, no TLS, zero config).
// If APP_TLS_CERT and APP_TLS_KEY are set, uses HTTP/2 with TLS.
// Handles SIGTERM/SIGINT for graceful shutdown — drains active connections.
func (g *Gateway) Serve(version string) error {
	if err := g.Router.Validate(); err != nil {
		return err
	}
	mux, port := g.buildMux(version)
	addr := ":" + port

	// Start metrics collection + gateway registration if Studio is configured
	g.metrics = NewMetricsCollector()
	if g.metrics != nil {
		g.Router.SetMetricsCollector(g.metrics)
	}
	g.registrar = newGatewayRegistrar(port, version)

	certFile := envOr("APP_TLS_CERT", "")
	keyFile := envOr("APP_TLS_KEY", "")

	server, serveErr, err := startGatewayServer(addr, mux, certFile, keyFile)
	if err != nil {
		return err
	}
	if certFile != "" && keyFile != "" {
		fmt.Printf("  Listening on https://localhost%s (HTTP/2 + TLS)\n\n", addr)
	} else {
		fmt.Printf("  Listening on http://localhost%s (HTTP/2 h2c)\n\n", addr)
	}

	g.server = server
	return g.waitForShutdown(serveErr)
}

func startGatewayServer(addr string, mux http.Handler, certFile, keyFile string) (*http.Server, <-chan error, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	server := &http.Server{Addr: addr, Handler: h2c.NewHandler(mux, &http2.Server{})}
	if certFile != "" && keyFile != "" {
		certificate, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
		if loadErr != nil {
			listener.Close()
			return nil, nil, fmt.Errorf("load TLS certificate: %w", loadErr)
		}
		server.Handler = mux
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		if configureErr := http2.ConfigureServer(server, &http2.Server{}); configureErr != nil {
			listener.Close()
			return nil, nil, fmt.Errorf("configure HTTP/2: %w", configureErr)
		}
		listener = tls.NewListener(listener, server.TLSConfig)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	return server, serveErr, nil
}

// waitForShutdown blocks until SIGTERM, SIGINT, or a server failure, then gracefully shuts down.
func (g *Gateway) waitForShutdown(serveErr <-chan error) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(quit)
	var sig os.Signal
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case sig = <-quit:
	}
	fmt.Printf("\n  Received %s, shutting down...\n", sig)

	timeout := 30 * time.Second
	if v := envOr("SHUTDOWN_TIMEOUT", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if g.metrics != nil {
		g.metrics.Close()
	}
	if g.registrar != nil {
		g.registrar.Close()
	}
	if err := g.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	fmt.Println("  Server stopped gracefully.")
	return nil
}

// buildMux creates the HTTP mux with all routes and middleware.
// Returns the mux and the port string. Extracted for testability.
func (g *Gateway) buildMux(version string) (*http.ServeMux, string) {
	g.Router.Version = version
	port := envOr("APP_PORT", "4000")
	mode := envOr("DEPLOY_MODE", "embedded")

	// Build DB display string from DATABASE_* fields
	dbHost := envOr("DATABASE_HOST", "")
	dbPort := envOr("DATABASE_PORT", "5432")
	dbUser := envOr("DATABASE_USER", "")
	dbPrefix := envOr("DATABASE_PREFIX", "")
	dbDisplay := ""
	if dbHost != "" {
		dbDisplay = fmt.Sprintf("%s@%s:%s/%s", dbUser, dbHost, dbPort, dbPrefix)
	}

	printBanner(version, port, mode, dbDisplay, g.modules)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)

	// i18n — load translations (non-fatal if missing)
	translator := i18n.New(envOr("APP_LOCALE", "en"))
	if err := translator.LoadDir("i18n"); err == nil {
		g.Router.SetTranslator(translator)
	}

	// Dev mode
	if envOr("APP_ENV", "") == "development" {
		g.Router.SetDevMode(true)
	}
	configureWebSocketOrigins(g.Router, envOr("CORS_ORIGIN", "*"))

	// Schema introspection key
	if key := envOr("INTROSPECTION_KEY", ""); key != "" {
		g.Router.IntrospectionKey = key
	}

	// Middleware chain: CORS → trace → auth → router
	var handler http.Handler = g.Router
	handler = api.TraceMiddleware(handler)
	jwtCfg, err := auth.LoadConfig()
	if err == nil {
		handler = AuthMiddleware(jwtCfg, handler)
	}
	handler = corsMiddleware(handler)
	mux.Handle("/luvia", handler)

	return mux, port
}

func configureWebSocketOrigins(router *api.Router, configured string) {
	configured = strings.TrimSpace(configured)
	if configured == "*" {
		router.WSAllowAllOrigins = true
		router.WSOrigins = nil
		return
	}
	origin, err := url.Parse(configured)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil {
		return
	}
	router.WSAllowAllOrigins = false
	router.WSOrigins = []string{origin.Host}
}

// corsMiddleware handles CORS preflight, response headers, and security headers (XSS, CSP, etc.).
// Configurable via CORS_ORIGIN env var (default: "*").
func corsMiddleware(next http.Handler) http.Handler {
	origin := envOr("CORS_ORIGIN", "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Luxo-Mode, X-Request-Id")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Security headers — XSS, clickjacking, MIME sniffing protection
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func envOr(key, fallback string) string {
	return env.GetOrDefault(key, fallback)
}

func printBanner(version, port, mode, dbURL string, modules []string) {
	fmt.Print(buildBanner(version, port, mode, dbURL, modules))
}

// buildBanner builds the full banner string without printing it.
func buildBanner(version, port, mode, dbURL string, modules []string) string {
	rgb := func(r, g, b int) string {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	}
	reset := "\033[0m"
	dim := "\033[2m"
	bold := "\033[1m"
	gold := rgb(245, 166, 35)
	purple := rgb(142, 68, 173)
	red := rgb(231, 76, 60)

	var sb strings.Builder

	banner := []string{
		" █     █   █ █   █  ███ ",
		" █     █   █  █ █  █   █",
		" █     █   █   █   █   █",
		" █     █   █  █ █  █   █",
		" ████   ███  █   █  ███ ",
	}
	sb.WriteByte('\n')
	for _, line := range banner {
		sb.WriteString(buildGradientLine(line, rgb))
		sb.WriteString(reset)
		sb.WriteByte('\n')
	}
	fmt.Fprintf(&sb, "  %sVersion%s  %s\n\n", dim, reset, version)

	fmt.Fprintf(&sb, "  %sMode%s       %s\n", dim, reset, mode)
	fmt.Fprintf(&sb, "  %sPort%s       %s%s%s\n", dim, reset, bold, port, reset)

	if dbURL != "" {
		fmt.Fprintf(&sb, "  %sDatabase%s   %s%s%s\n", dim, reset, purple, maskDSN(dbURL), reset)
	} else {
		fmt.Fprintf(&sb, "  %sDatabase%s   %s(not configured)%s\n", dim, reset, red, reset)
	}

	fmt.Fprintf(&sb, "\n  %s%sLuvia Gateway%s\n", bold, gold, reset)

	if len(modules) == 0 {
		fmt.Fprintf(&sb, "    %s(no modules)%s\n", dim, reset)
	}
	for _, m := range modules {
		fmt.Fprintf(&sb, "    %s->%s %s\n", gold, reset, m)
	}

	sb.WriteByte('\n')
	return sb.String()
}

// buildGradientLine builds a gradient-colored line string without printing it.
func buildGradientLine(line string, rgb func(int, int, int) string) string {
	type color struct{ r, g, b int }
	stops := []color{
		{0x2C, 0x3E, 0x50},
		{0x8E, 0x44, 0xAD},
		{0xE7, 0x4C, 0x3C},
		{0xF5, 0xA6, 0x23},
	}
	n := len(line)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return rgb(stops[0].r, stops[0].g, stops[0].b) + line
	}
	var sb strings.Builder
	for i, ch := range line {
		t := float64(i) / float64(n-1) * float64(len(stops)-1)
		seg := int(t)
		if seg >= len(stops)-1 {
			seg = len(stops) - 2
		}
		f := t - float64(seg)
		r := int(float64(stops[seg].r)*(1-f) + float64(stops[seg+1].r)*f)
		g := int(float64(stops[seg].g)*(1-f) + float64(stops[seg+1].g)*f)
		b := int(float64(stops[seg].b)*(1-f) + float64(stops[seg+1].b)*f)
		fmt.Fprintf(&sb, "%s%c", rgb(r, g, b), ch)
	}
	return sb.String()
}

func maskDSN(dsn string) string {
	at := strings.Index(dsn, "@")
	if at < 0 {
		return dsn
	}
	prefix := dsn[:at]
	colon := strings.LastIndex(prefix, ":")
	if colon < 0 {
		return dsn
	}
	return prefix[:colon+1] + "***" + dsn[at:]
}

// DiscoverModules finds module names from origin/*.luxo files.
func DiscoverModules() []string {
	entries, err := os.ReadDir("origin")
	if err != nil {
		return nil
	}
	var modules []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".luxo") {
			name := strings.TrimSuffix(e.Name(), ".luxo")
			modules = append(modules, name)
		}
	}
	return modules
}
