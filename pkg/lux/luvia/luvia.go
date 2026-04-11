package luvia

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/env"
	"github.com/light-speak/luxo/pkg/lux/i18n"
)

// Gateway is the Luvia API gateway.
type Gateway struct {
	Router  *api.Router
	modules []string
}

// New creates a new Luvia gateway.
func New() *Gateway {
	return &Gateway{
		Router: api.NewRouter(),
	}
}

// AddModule registers a module name for display.
func (g *Gateway) AddModule(name string) {
	g.modules = append(g.modules, name)
}

// Serve starts the HTTP server with banner display.
func (g *Gateway) Serve(version string) error {
	mux, port := g.buildMux(version)

	addr := ":" + port
	fmt.Printf("  Listening on http://localhost%s\n\n", addr)

	return http.ListenAndServe(addr, mux)
}

// buildMux creates the HTTP mux with all routes and middleware.
// Returns the mux and the port string. Extracted for testability.
func (g *Gateway) buildMux(version string) (*http.ServeMux, string) {
	port := envOr("APP_PORT", "4000")
	mode := envOr("DEPLOY_MODE", "embedded")
	transport := envOr("TRANSPORT_MODE", "json")

	// Build DB display string from DATABASE_* fields
	dbHost := envOr("DATABASE_HOST", "")
	dbPort := envOr("DATABASE_PORT", "5432")
	dbUser := envOr("DATABASE_USER", "")
	dbPrefix := envOr("DATABASE_PREFIX", "")
	dbDisplay := ""
	if dbHost != "" {
		dbDisplay = fmt.Sprintf("%s@%s:%s/%s", dbUser, dbHost, dbPort, dbPrefix)
	}

	printBanner(version, port, mode, transport, dbDisplay, g.modules)

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

	// Middleware chain: trace → auth → router
	var handler http.Handler = g.Router
	handler = api.TraceMiddleware(handler)
	jwtCfg, err := auth.LoadConfig()
	if err == nil {
		handler = AuthMiddleware(jwtCfg, handler)
	}
	mux.Handle("/luvia", handler)

	return mux, port
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func envOr(key, fallback string) string {
	return env.GetOrDefault(key, fallback)
}

func printBanner(version, port, mode, transport, dbURL string, modules []string) {
	fmt.Print(buildBanner(version, port, mode, transport, dbURL, modules))
}

// buildBanner builds the full banner string without printing it.
func buildBanner(version, port, mode, transport, dbURL string, modules []string) string {
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
	fmt.Fprintf(&sb, "  %sTransport%s  %s\n", dim, reset, transport)
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
