package luvia

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/env"
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
	port := envOr("APP_PORT", "4000")
	mode := envOr("DEPLOY_MODE", "embedded")
	transport := envOr("TRANSPORT_MODE", "json")
	dbURL := envOr("DATABASE_URL", "")

	printBanner(version, port, mode, transport, dbURL, g.modules)

	mux := http.NewServeMux()
	mux.Handle("/luvia", g.Router)
	mux.HandleFunc("/health", handleHealth)

	addr := ":" + port
	fmt.Printf("  Listening on http://localhost%s\n\n", addr)

	return http.ListenAndServe(addr, mux)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func envOr(key, fallback string) string {
	if v, ok := env.Get(key); ok {
		return v
	}
	return fallback
}

func printBanner(version, port, mode, transport, dbURL string, modules []string) {
	rgb := func(r, g, b int) string {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	}
	reset := "\033[0m"
	dim := "\033[2m"
	bold := "\033[1m"
	gold := rgb(245, 166, 35)
	purple := rgb(142, 68, 173)
	red := rgb(231, 76, 60)

	banner := []string{
		" █     █   █ █   █  ███ ",
		" █     █   █  █ █  █   █",
		" █     █   █   █   █   █",
		" █     █   █  █ █  █   █",
		" ████   ███  █   █  ███ ",
	}
	fmt.Println()
	for _, line := range banner {
		gradientLine(line, rgb)
		fmt.Println(reset)
	}
	fmt.Printf("  %sVersion%s  %s\n\n", dim, reset, version)

	fmt.Printf("  %sMode%s       %s\n", dim, reset, mode)
	fmt.Printf("  %sTransport%s  %s\n", dim, reset, transport)
	fmt.Printf("  %sPort%s       %s%s%s\n", dim, reset, bold, port, reset)

	if dbURL != "" {
		fmt.Printf("  %sDatabase%s   %s%s%s\n", dim, reset, purple, maskDSN(dbURL), reset)
	} else {
		fmt.Printf("  %sDatabase%s   %s(not configured)%s\n", dim, reset, red, reset)
	}

	fmt.Printf("\n  %s%sLuvia Gateway%s\n", bold, gold, reset)

	if len(modules) == 0 {
		fmt.Printf("    %s(no modules)%s\n", dim, reset)
	}
	for _, m := range modules {
		fmt.Printf("    %s->%s %s\n", gold, reset, m)
	}

	fmt.Println()
}

func gradientLine(line string, rgb func(int, int, int) string) {
	type color struct{ r, g, b int }
	stops := []color{
		{0x2C, 0x3E, 0x50},
		{0x8E, 0x44, 0xAD},
		{0xE7, 0x4C, 0x3C},
		{0xF5, 0xA6, 0x23},
	}
	n := len(line)
	if n == 0 {
		return
	}
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
		fmt.Printf("%s%c", rgb(r, g, b), ch)
	}
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
