package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/str"
)

// ScaffoldResult holds scaffold files to be written to disk.
// Keys are relative paths from the project root.
type ScaffoldResult struct {
	Files map[string][]byte
}

// InitProject generates the project scaffold for `luxo init <projectName>`.
// Creates the bare project structure with no modules.
func InitProject(modulePath string) *ScaffoldResult {
	sr := &ScaffoldResult{
		Files: make(map[string][]byte),
	}

	sr.Files[".gitignore"] = generateGitignore()
	sr.Files[".env.example"] = generateEnvExample()
	sr.Files["go.mod"] = generateGoMod(modulePath)

	return sr
}

// AddModule generates scaffold files for `luxo add <moduleName>`.
// Creates the module schema, resolver, and cmd entry.
func AddModule(modulePath, moduleName string) *ScaffoldResult {
	sr := &ScaffoldResult{
		Files: make(map[string][]byte),
	}

	sr.Files[fmt.Sprintf("origin/%s.luxo", moduleName)] = generateModuleSchema(moduleName)
	sr.Files[fmt.Sprintf("service/%s/resolver/resolver.go", moduleName)] = generateModuleResolver(modulePath, moduleName)
	sr.Files[fmt.Sprintf("luxis/%s/main.go", moduleName)] = generateModuleMain(modulePath, moduleName)

	return sr
}

// generateGitignore produces the .gitignore file.
func generateGitignore() []byte {
	return []byte(`# Environment
.env
.env.local

# Go
bin/
vendor/

# Luxo generated (uncomment to exclude from git)
# */luxo/*.gen.go

# IDE
.idea/
.vscode/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Test
*.cov
*.out

# Build
dist/
tmp/
`)
}

// generateGoMod produces the go.mod file.
// Does not include luxo dependency — added automatically by `go mod tidy` after `luxo gen`.
func generateGoMod(modulePath string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\n", modulePath)
	b.WriteString("go 1.26.0\n")
	return []byte(b.String())
}

// generateModuleSchema produces the origin/<module>.luxo skeleton.
func generateModuleSchema(moduleName string) []byte {
	name := str.Capitalize(moduleName)
	var b strings.Builder
	fmt.Fprintf(&b, "// %s module\n\n", name)
	fmt.Fprintf(&b, "model %s {\n", name)
	b.WriteString("  id:        UUID      @auto\n")
	b.WriteString("  createdAt: DateTime\n")
	b.WriteString("  updatedAt: DateTime\n")
	b.WriteString("}\n")
	return []byte(b.String())
}

// generateModuleResolver produces the <module>/resolver/resolver.go scaffold.
func generateModuleResolver(modulePath, moduleName string) []byte {
	luxoPkg := fmt.Sprintf("%s/service/%s/luxo", modulePath, moduleName)

	var b strings.Builder
	b.WriteString("package resolver\n\n")
	fmt.Fprintf(&b, "import luxo %q\n\n", luxoPkg)
	fmt.Fprintf(&b, "// Resolver holds custom dependencies for the %s module.\n", moduleName)
	b.WriteString("type Resolver struct {\n")
	b.WriteString("\t// Add your dependencies here.\n")
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "// Setup initializes the %s module's custom dependencies.\n", moduleName)
	b.WriteString("func Setup(app *luxo.App) {\n")
	b.WriteString("\t// Register middleware, hooks, etc.\n")
	b.WriteString("}\n")
	return []byte(b.String())
}

// generateModuleMain produces the cmd/<module>/main.go entry point.
func generateModuleMain(modulePath, moduleName string) []byte {
	pkgName := moduleName
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"os\"\n")
	b.WriteString("\n")
	fmt.Fprintf(&b, "\t\"%s/service/%s/luxo\"\n", modulePath, pkgName)
	fmt.Fprintf(&b, "\t\"%s/service/%s/resolver\"\n", modulePath, pkgName)
	b.WriteString("\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/env\"\n")
	b.WriteString(")\n\n")

	b.WriteString("func main() {\n")
	b.WriteString("\tctx := context.Background()\n\n")
	b.WriteString("\tif err := env.Load(\".env\"); err != nil {\n")
	b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"warning: %v\\n\", err)\n")
	b.WriteString("\t}\n\n")
	b.WriteString("\tapp, err := luxo.New(ctx)\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"fatal: %v\\n\", err)\n")
	b.WriteString("\t\tos.Exit(1)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tdefer app.Close()\n\n")
	b.WriteString("\tresolver.Setup(app)\n\n")
	b.WriteString("\t// TODO: start service\n")
	b.WriteString("\t_ = app\n")
	b.WriteString("}\n")

	return []byte(b.String())
}

// generateEnvExample produces the .env.example file with all config items.
func generateEnvExample() []byte {
	return []byte(`# ============================================================
# Luxo Environment Configuration
# Copy this file to .env and fill in your values.
# System environment variables take precedence over .env values.
# ============================================================

# ---- App ----
APP_ENV=development                                    # environment: development / production
APP_DEBUG=true                                         # debug logging
APP_PORT=8080                                          # Luvia Gateway port (HTTP/2 h2c by default)
APP_TIMEZONE=UTC                                       # timezone: UTC / Asia/Shanghai
DEPLOY_MODE=embedded                                   # deploy: embedded / standalone / compose / cluster
GEN_MODE=full                                          # codegen: full / minimal (minimal = model + db only)

# ---- TLS (optional) ----
# Without these, uses h2c (HTTP/2 cleartext, recommended for internal networks)
# APP_TLS_CERT=./certs/server.crt                       # TLS certificate file path
# APP_TLS_KEY=./certs/server.key                        # TLS private key file path

# ---- Luxo Protocol ----
# Auto-enabled in compose/cluster mode. All inter-service calls use Luxo protocol.
LUXO_PORT=9000                                         # Luxo protocol port

# ---- Database ----
DATABASE_DRIVER=pg                                     # driver: pg / mysql / sqlite / mongo
DATABASE_PREFIX=myapp                                  # db name prefix
DATABASE_HOST=localhost                                # host
DATABASE_PORT=5432                                     # port
DATABASE_USER=postgres                                 # username
DATABASE_PASSWORD=secret                               # password
DATABASE_POOL=20                                       # max connections
DATABASE_IDLE=5                                        # idle connections
DATABASE_TIMEOUT=5s                                    # connection timeout
DATABASE_SSL=disable                                   # SSL: disable / require / verify-full

# ---- Debug ----
# DEBUG_SQL=true                                        # print all SQL queries with timing

# ---- Auth (JWT) ----
JWT_SECRET=change-me                                   # HMAC signing key
JWT_EXPIRES=7d                                         # token expiry
JWT_REFRESH=true                                       # enable refresh tokens
JWT_REFRESH_EXPIRES=30d                                # refresh token expiry

# ---- Log ----
LOG_LEVEL=info                                         # level: debug / info / warn / error
LOG_FORMAT=text                                        # format: json / text

# ---- Gateway (Luvia) ----
CORS_ORIGINS=*                                         # allowed origins
CORS_METHODS=GET,POST,PUT,DELETE                       # allowed methods
CORS_HEADERS=Authorization,Content-Type                # allowed headers
CORS_CREDENTIALS=true                                  # allow credentials
CORS_MAX_AGE=24h                                       # preflight cache

RATE_LIMIT_WINDOW=1m                                   # rate limit window
RATE_LIMIT_MAX=100                                     # max requests per client per window

TIMEOUT_READ=30s                                       # read timeout
TIMEOUT_WRITE=30s                                      # write timeout
TIMEOUT_IDLE=120s                                      # idle timeout

MAX_DEPTH=10                                           # max field nesting depth
MAX_FIELDS=50                                          # max fields per request
MAX_BODY_SIZE=1mb                                      # max request body size
MAX_BATCH=10                                           # max batch count

TRANSPORT_MODE=auto                                    # transport: auto / json / binary
HEALTH_PATH=/health                                    # health check endpoint
GRACEFUL_TIMEOUT=30s                                   # graceful shutdown timeout
REQUEST_ID_HEADER=X-Request-Id                         # request ID header
`)
}
