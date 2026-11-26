package codegen

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

const statelessServiceSource = `
fn identity(value: Int): Int { return value }
fn twice(value: Int): Int @service { return identity(value) * 2 }
api computed(value: Int): Int { return identity(value) + 1 }
`

const statelessEventSource = `
event Requested(value: Int)
event Counted(value: Int)
on Requested { emit Counted(value: it.value) }
fn deliver(value: Int): Int { emit Requested(value: value) return value }
fn forward(value: Int): Int @service { return deliver(value) }
api publish(value: Int): Int { return deliver(value) }
`

func statelessEventIDs() StableIDs {
	return StableIDs{EventFields: map[string]map[string]int{
		"Requested": {"value": 1}, "Counted": {"value": 1},
	}}
}

func TestStatelessEventsDoNotRequireDatabase(t *testing.T) {
	for _, source := range []string{
		`event Requested(value: Int)`,
		`event Requested(value: Int) on Requested { val value = it.value }`,
		statelessEventSource,
	} {
		input := analyzePermissionFixture(t, source)
		input.Files[0].Name = "origin/logic/events.luxo"
		if appUsesDatabase(input) || modulesNeedDatabase(collectModules(input)) {
			t.Errorf("event-only operations require a database: %s", source)
		}
		output, err := Generate(input, "luxo", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, statelessEventIDs())})
		if err != nil {
			t.Fatal(err)
		}
		app := string(output.Files["app.gen.go"])
		if strings.Contains(app, "lux/pg") || !strings.Contains(app, "EventBus event.Bus") {
			t.Errorf("invalid event-only App:\n%s", app)
		}
	}
}

func TestGeneratedStatelessEventDispatch(t *testing.T) {
	runGeneratedFixture(t, statelessEventSource, statelessEventAssertions, statelessEventIDs())
}

func TestStatelessEventBackendsRetainExplicitSelection(t *testing.T) {
	input := analyzePermissionFixture(t, statelessEventSource)
	input.Files[0].Name = "origin/logic/events.luxo"
	if _, err := GenerateEntryFileChecked(input, "example.test/events"); err == nil {
		t.Fatal("event backend was selected implicitly")
	}
	for _, backend := range []string{"memory", "nats"} {
		selection := EntryBackends{Events: backend}
		embedded, err := GenerateEntryFileChecked(input, "example.test/events", selection)
		if err != nil {
			t.Fatal(err)
		}
		cluster, err := GenerateModuleEntryFilesChecked(input, "example.test/events", selection)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{string(embedded), string(cluster["logic"])} {
			if strings.Contains(source, "lux/pg") || !strings.Contains(source, "logic_luxo.New(ctx)") {
				t.Fatalf("event backend %s retained database setup:\n%s", backend, source)
			}
			if strings.Contains(source, "lux/nats/event") != (backend == "nats") {
				t.Fatalf("event backend %s changed build selection:\n%s", backend, source)
			}
		}
	}
}

const statelessEventAssertions = `package generated
import("bytes";"context";"encoding/json";"net";"net/http/httptest";"testing";"time";
 "github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
func TestEventOnlyDispatch(t *testing.T){
 t.Setenv("DATABASE_HOST","127.0.0.1");t.Setenv("DATABASE_PORT","1")
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 bus:=event.NewChanBus(8);defer func(){if err:=bus.Close();err!=nil{t.Error(err)}}();app.EventBus=bus
 observed:=make(chan int64,4)
 if err:=event.OnDecode(bus,"Counted",UnmarshalCounted,func(_ context.Context,payload CountedEvent)error{observed<-payload.Value;return nil});err!=nil{t.Fatal(err)}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 check:=func(){t.Helper();select{case value:=<-observed:if value!=42{t.Fatal(value)};case<-time.After(time.Second):t.Fatal("event not observed")}}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"publish\",\"value\":42}")
  if binary{enc:=codec.NewEncoder(codec.AppendVarint(codec.AppendVarint(nil,uint64(router.Schema.APIs["publish"].ID)),0));enc.WriteFieldInt(1,42);enc.WriteEnd();body=enc.Bytes()}
  req:=httptest.NewRequest("POST","/",bytes.NewReader(body));if binary{req.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,req);if response.Code!=200{t.Fatal(binary,response.Code,response.Body.String())}
  if binary{if !bytes.Equal(response.Body.Bytes(),codec.AppendSvarint(nil,42)){t.Fatal(response.Body.Bytes())}}else{var envelope struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||envelope.Data!=42{t.Fatal(envelope.Data,err)}};check()
 }
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 var params codec.Encoder;params.WriteFieldInt(1,42);params.WriteEnd()
 got,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs["svc:forward"].ID,nil,params.Bytes());if err!=nil||!bytes.Equal(got,codec.AppendSvarint(nil,42)){t.Fatal(got,err)};check()
 app.Close();if _,err:=app.deliver(t.Context(),42);err!=nil{t.Fatal(err)};check()
}
`

func TestGeneratedStatelessServiceDispatch(t *testing.T) {
	runGeneratedFixture(t, statelessServiceSource, `package generated
import("bytes";"encoding/json";"net";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc")
func TestStatelessDispatch(t *testing.T){
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=RegisterServiceFns(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"computed\",\"value\":41}")
  if binary{encoder:=codec.NewEncoder(codec.AppendVarint(codec.AppendVarint(nil,uint64(router.Schema.APIs["computed"].ID)),0));encoder.WriteFieldInt(1,41);encoder.WriteEnd();body=encoder.Bytes()}
  req:=httptest.NewRequest("POST","/",bytes.NewReader(body));if binary{req.Header.Set("X-Luxo-Mode","binary")}
  out:=httptest.NewRecorder();router.ServeHTTP(out,req);if out.Code!=200{t.Fatal(binary,out.Code,out.Body.String())}
  if binary{if !bytes.Equal(out.Body.Bytes(),codec.AppendSvarint(nil,42)){t.Fatal(out.Body.Bytes())}}else{var envelope struct{Data int64};if err:=json.Unmarshal(out.Body.Bytes(),&envelope);err!=nil||envelope.Data!=42{t.Fatal(out.Body.String(),err)}}
 }
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 var params codec.Encoder;params.WriteFieldInt(1,21);params.WriteEnd()
 body,err:=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs["svc:twice"].ID,nil,params.Bytes())
 if err!=nil||!bytes.Equal(body,codec.AppendSvarint(nil,42)){t.Fatal(body,err)}
}
`, StableIDs{})
}

func TestStatelessEntryPackagesBuildWithoutDrivers(t *testing.T) {
	assertStatelessEntryPackages(t, statelessServiceSource, EntryBackends{}, "computed", 41)
}

func TestStatelessEventEntryPackagesBuildWithoutDrivers(t *testing.T) {
	assertStatelessEntryPackages(t, statelessEventSource, EntryBackends{Events: "memory"}, "publish", 42)
}

func assertStatelessEntryPackages(t *testing.T, source string, backends EntryBackends, api string, value int) {
	t.Helper()
	input := analyzePermissionFixture(t, source)
	input.Files[0].Name = "origin/logic/functions.luxo"
	dir := writeProtocolFixture(t, "package generated\n", "package generated\n")
	modulePath := "github.com/light-speak/luxo/.tmp/" + filepath.Base(dir)
	output, err := Generate(input, "luxo", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, statelessEventIDs())})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range output.Files {
		writeStatelessFixtureFile(t, dir, "service/logic/luxo/"+name, data)
	}
	writeStatelessFixtureFile(t, dir, "service/logic/resolver/setup.go", []byte("package resolver\nimport logic \""+modulePath+"/service/logic/luxo\"\nfunc Setup(app *logic.App){}\n"))
	embedded, err := GenerateEntryFileChecked(input, modulePath, backends)
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := GenerateModuleEntryFilesChecked(input, modulePath, backends)
	if err != nil {
		t.Fatal(err)
	}
	writeStatelessFixtureFile(t, dir, "lucis/app/main.go", embedded)
	writeStatelessFixtureFile(t, dir, "lucis/logic/main.go", cluster["logic"])
	build := exec.Command("go", "build", "./lucis/...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("stateless entry build: %v\n%s", err, out)
	}
	deps := exec.Command("go", "list", "-deps", "./lucis/...")
	deps.Dir = dir
	out, err := deps.CombinedOutput()
	if err != nil {
		t.Fatalf("stateless dependencies: %v\n%s", err, out)
	}
	for _, dependency := range []string{"github.com/jackc/", "/lux/pg", "/lux/nats", "/lux/cache/redis", "github.com/aws/"} {
		if strings.Contains(string(out), dependency) {
			t.Errorf("unused adapter included: %s", dependency)
		}
	}
	for _, target := range []string{"app", "logic"} {
		t.Run(target, func(t *testing.T) { assertStatelessEntryStarts(t, dir, target, backends, api, value) })
	}
}

func assertStatelessEntryStarts(t *testing.T, dir, target string, backends EntryBackends, api string, value int) {
	t.Helper()
	binary := filepath.Join(dir, "entry-"+target)
	args := []string{"build", "-o", binary}
	if os.Getenv("LUXO_TRANSACTION_RACE") == "1" {
		args = append(args, "-race")
	}
	build := exec.Command("go", append(args, "./lucis/"+target)...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("entry binary: %v\n%s", err, out)
	}
	logPath := filepath.Join(dir, "entry-"+target+".log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	command := exec.Command(binary)
	command.Dir, command.Stdout, command.Stderr = dir, log, log
	command.Env = statelessEntryEnvironment(backends)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	finished := false
	t.Cleanup(func() { stopStatelessEntry(t, command, done, finished, logPath) })
	port := waitStatelessEntryPort(t, done, &finished, logPath)
	probeStatelessEntry(t, port, api, value)
}

func statelessEntryEnvironment(backends EntryBackends) []string {
	var result []string
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "APP_") || strings.HasPrefix(name, "DATABASE_") ||
			strings.HasPrefix(name, "JWT_") || strings.HasPrefix(name, "LUXO_") ||
			name == "DEPLOY_MODE" || name == "AUTO_MIGRATE" || name == "GRACEFUL_TIMEOUT" || name == "LOG_REQUESTS" {
			continue
		}
		result = append(result, item)
	}
	return append(result, "APP_PORT=0", "DEPLOY_MODE=embedded", "AUTO_MIGRATE=true",
		"DATABASE_HOST=127.0.0.1", "DATABASE_PORT=1", "LUXO_EVENT_BACKEND="+backends.Events,
		"GRACEFUL_TIMEOUT=1s", "LOG_REQUESTS=false")
}

func stopStatelessEntry(t *testing.T, command *exec.Cmd, done <-chan error, finished bool, logPath string) {
	t.Helper()
	if finished {
		return
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			out, _ := os.ReadFile(logPath)
			t.Errorf("entry exit: %v\n%s", err, out)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Error("entry did not stop after SIGTERM")
	}
}

func waitStatelessEntryPort(t *testing.T, done <-chan error, finished *bool, logPath string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			*finished = true
			out, _ := os.ReadFile(logPath)
			t.Fatalf("entry stopped before request: %v\n%s", err, out)
		default:
		}
		out, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, reported, found := strings.Cut(string(out), "\n  Listening on http://localhost:"); found {
			if digits, _, complete := strings.Cut(reported, " "); complete {
				port, err := strconv.ParseUint(digits, 10, 16)
				if err != nil || port == 0 {
					t.Fatalf("entry reported invalid listening port %q", digits)
				}
				return int(port)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	out, _ := os.ReadFile(logPath)
	t.Fatalf("entry never reported its listening port:\n%s", out)
	return 0
}

func probeStatelessEntry(t *testing.T, port int, api string, value int) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port) + "/luvia"
	body := `{"$api":"` + api + `","value":` + strconv.Itoa(value) + `}`
	response, err := client.Post(endpoint, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	var envelope struct{ Data int64 }
	decodeErr := json.Unmarshal(data, &envelope)
	if readErr != nil || decodeErr != nil || response.StatusCode != 200 || envelope.Data != 42 {
		t.Fatalf("entry response: %d %s %v %v", response.StatusCode, data, readErr, decodeErr)
	}
}

func writeStatelessFixtureFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedFunctionOnlyApplication(t *testing.T) {
	runGeneratedFixture(t, `fn identity(value: Int): Int { return value }`, `package generated
import "testing"
func TestFunctionOnly(t *testing.T){
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)};defer app.Close()
 if value,err:=app.identity(t.Context(),42);value!=42||err!=nil{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedStatelessAppContainsNoDriver(t *testing.T) {
	for _, source := range []string{
		`fn identity(value: Int): Int { return value }`,
		`api identity(value: Int): Int { return value }`,
		`fn twice(value: Int): Int @service { return value * 2 }`,
	} {
		input := analyzePermissionFixture(t, source)
		ids := completeFixtureAPIIDs(input, StableIDs{})
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		app := string(output.Files["app.gen.go"])
		if !strings.Contains(app, "type App struct") || !strings.Contains(app, "func New(") {
			t.Fatalf("missing stateless app: %s", app)
		}
		for name, file := range output.Files {
			if strings.Contains(string(file), "lux/pg") || strings.Contains(string(file), "pgx") {
				t.Fatalf("stateless module imported a driver in %s", name)
			}
		}
	}
}

func TestStatelessEntriesSkipDatabase(t *testing.T) {
	input := analyzePermissionFixture(t, `fn twice(value: Int): Int @service { return value * 2 }`)
	input.Files[0].Name = "origin/logic/functions.luxo"
	embedded, err := GenerateEntryFileChecked(input, "example.test/stateless")
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := GenerateModuleEntryFilesChecked(input, "example.test/stateless")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{string(embedded), string(cluster["logic"])} {
		for _, forbidden := range []string{"lux/pg", "NewFromDB", "DBConfigFromEnv", "EnsureDatabase", "app.DB"} {
			if strings.Contains(entry, forbidden) {
				t.Errorf("stateless entry contains %s", forbidden)
			}
		}
		for _, required := range []string{"logic_luxo.New(ctx)", "RegisterSchema", "RegisterServiceFns"} {
			if !strings.Contains(entry, required) {
				t.Errorf("stateless entry omitted %s", required)
			}
		}
	}
}

func TestAppRetainsDatabaseForTransactionAndNative(t *testing.T) {
	for _, source := range []string{
		`fn read(): Result<Int> @native`,
		`fn read(): Int { return transaction { 42 } }`,
		`fn read(flag: Boolean): Int { if flag { transaction { val value = 1 } } return 42 }`,
	} {
		input := analyzePermissionFixture(t, source)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{})})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(output.Files["app.gen.go"]), "DB *pg.DB") {
			t.Fatalf("database dependency dropped: %s", source)
		}
	}
}

func TestMixedEntriesBindOnlyDatabaseModules(t *testing.T) {
	logic := analyzePermissionFixture(t, statelessServiceSource)
	logic.Files[0].Name = "origin/logic/functions.luxo"
	stored := analyzePermissionFixture(t, `model Record @noTime { id: Int @id }`)
	stored.Files[0].Name = "origin/stored/model.luxo"
	input := semantic.Analyze(append(logic.Files, stored.Files...))
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	embedded, err := GenerateEntryFileChecked(input, "example.test/mixed")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"pg.NewDBWithConfig", "stored_luxo.NewFromDB(db)", "logic_luxo.New(ctx)"} {
		if !strings.Contains(string(embedded), required) {
			t.Errorf("mixed entry omitted %s", required)
		}
	}
	cluster, err := GenerateModuleEntryFilesChecked(input, "example.test/mixed")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cluster["logic"]), "lux/pg") || !strings.Contains(string(cluster["stored"]), "lux/pg/migrate") {
		t.Fatal("mixed cluster entries lost database isolation")
	}
}

func TestAppDatabaseRequirementsIncludeEveryBody(t *testing.T) {
	transaction := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.TransactionExpr{Body: &ast.Block{}}}}}
	for _, file := range []*ast.File{
		{APIs: []*ast.ApiDecl{{Body: transaction}}},
		{Middlewares: []*ast.MiddlewareDecl{{Body: transaction}}},
		{Listeners: []*ast.OnDecl{{Body: transaction}}},
		{Listeners: []*ast.OnDecl{{Native: true}}},
	} {
		if !fileAppUsesDatabase(file) {
			t.Fatal("transaction dependency dropped")
		}
	}
	if fileAppUsesDatabase(&ast.File{Middlewares: []*ast.MiddlewareDecl{{Body: &ast.Block{}}}}) {
		t.Fatal("empty middleware unexpectedly requires a database")
	}
	legacy := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "transaction"}}}}}
	if !bodyContainsTransaction(legacy) {
		t.Fatal("internal transaction-call AST lost detection")
	}
}

func TestStatelessAppEventBindingOwnership(t *testing.T) {
	runGeneratedProtocolPackage(t, string(generateStatelessAppFile("generated", true)), `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type ownedEventBus struct{closed,calls int}
func(b *ownedEventBus)Emit(context.Context,string,any)error{b.calls++;return nil}
func(*ownedEventBus)On(string,event.Handler)error{return nil}
func(*ownedEventBus)OnQueue(string,string,event.Handler)error{return nil}
func(b *ownedEventBus)Close()error{b.closed++;return nil}
func TestInjectedEventBus(t *testing.T){
 app,err:=New(t.Context());if err!=nil{t.Fatal(err)}
 bus:=&ownedEventBus{};app.EventBus=bus;app.Close()
 if bus.closed!=0{t.Fatal("App closed an injected resource")}
 if err:=app.EventBus.Emit(t.Context(),"probe",nil);err!=nil||bus.calls!=1{t.Fatal(err,bus.calls)}
}
`)
}
