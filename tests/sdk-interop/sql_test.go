package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSQLFixture(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(root, "sdk-sql-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(out) != root {
			t.Fatal("invalid scratch directory")
		}
		if err := os.RemoveAll(out); err != nil {
			t.Error(err)
		}
	})
	for _, invalid := range []string{"", root, filepath.Join(out, "missing")} {
		if err := generateSQLFixture(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if err := generateSQLFixture(out); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(out, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, fixtureSQLServer) {
		t.Fatal("wrong server template")
	}
	proxy, err := os.ReadFile(filepath.Join(out, "gateway_proxy.gen.go"))
	if err != nil || !bytes.Contains(proxy, []byte("func proxyHandler(")) {
		t.Fatalf("SQL fixture omitted the real generated gateway: %v", err)
	}
	if err := generateSQLFixture(out); err == nil {
		t.Fatal("overwrote existing output")
	}
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(out, "server"), out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	cmd = exec.CommandContext(t.Context(), filepath.Join(out, "server"))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "LUXO_TEST_DATABASE_URL=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("dedicated empty test database")) {
		t.Fatalf("missing database was not rejected: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(out, "capture_test.go"), []byte(sqlCaptureAssertions), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("capture counters: %v\n%s", err, output)
	}
	checkFixtureRegistrationErrors(t, "sql.go", fixtureSQLServer)
}

func TestGenerateRelationshipSQLFixture(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(root, "sdk-relations-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(out) != root {
			t.Fatal("unowned relationship fixture")
		}
		if err := os.RemoveAll(out); err != nil {
			t.Error(err)
		}
	})
	invalidCLI := exec.CommandContext(t.Context(), "go", "run", "./tests/sdk-interop", "-sql-relations", "-out", out, "unexpected")
	if output, err := invalidCLI.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("only accepts -out")) {
		t.Fatalf("accepted relationship fixture positional argument: %v\n%s", err, output)
	}
	for _, invalid := range []string{"", root, filepath.Join(out, "missing")} {
		if err := generateRelationshipSQLFixture(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	for index := range 7 {
		modes := make([]bool, 7)
		modes[index] = true
		if err := generateRelationshipSQLMode(out, nil, modes...); err == nil {
			t.Fatalf("accepted incompatible mode %d", index)
		}
	}
	if err := generateRelationshipSQLMode(out, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model.gen.go", "db.gen.go", "dataloader.gen.go", "handler.gen.go", "gateway_proxy.gen.go"} {
		if source, err := os.ReadFile(filepath.Join(out, name)); err != nil || len(source) == 0 {
			t.Fatalf("missing generated %s: %v", name, err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("relationship fixture: %v\n%s", err, output)
	}
	if err := generateRelationshipSQLFixture(out); err == nil {
		t.Fatal("overwrote generated relationship fixture")
	}
}

const sqlCaptureAssertions = `package main
import("context";"errors";"net/http/httptest";"sync";"testing";"time";"github.com/jackc/pgx/v5")
func TestSQLRequestMode(t *testing.T){
 for _,item:=range []struct{mode,content,want string}{
  {"json","application/json","json"},{"json","application/json; charset=utf-8","json"},
  {"binary","application/x-luxo","binary"},{"binary","application/x-luxo; version=1","binary"},
  {"json","application/x-luxo",""},{"binary","application/json",""},
  {"json","invalid; ;;",""},{"","application/json","json"},
 }{r:=httptest.NewRequest("POST","/luvia",nil);r.Header.Set("X-Luxo-Mode",item.mode);r.Header.Set("Content-Type",item.content)
  if got:=sqlRequestMode(r);got!=item.want{t.Fatal(item,got)}
 }
}
func TestConcurrentCapture(t *testing.T){
 var capture capture;var group sync.WaitGroup
 for range 8{group.Go(func(){for index:=range 100{capture.Record("ownProducts",0,index==0);capture.TraceQueryStart(context.Background(),nil,pgx.TraceQueryStartData{SQL:"SELECT name FROM products"})}})}
 group.Wait();calls,failed:=capture.counts();if calls["ownProducts"]!=800||failed!=8||len(capture.take())!=800{t.Fatal(calls,failed)}
 calls["ownProducts"]=0;again,failed:=capture.counts();if again["ownProducts"]!=800||failed!=8||len(capture.take())!=0{t.Fatal("snapshot changed captured state",again,failed)}
}
func TestCaptureTimings(t *testing.T){
 var disabled capture
 ctx:=disabled.TraceQueryStart(context.Background(),nil,pgx.TraceQueryStartData{SQL:"SELECT 1"})
 disabled.TraceQueryEnd(ctx,nil,pgx.TraceQueryEndData{})
 disabled.Record("test",time.Second,false);disabled.recordHTTP(time.Second)
 if disabled.takeTimings()!=nil{t.Fatal("disabled timing allocated a report")}
 enabled:=capture{diagnostic:true}
 enabled.TraceQueryEnd(context.Background(),nil,pgx.TraceQueryEndData{})
 slow:=enabled.TraceQueryStart(context.Background(),nil,pgx.TraceQueryStartData{SQL:"slow"})
 time.Sleep(2*time.Millisecond)
 fast:=enabled.TraceQueryStart(context.Background(),nil,pgx.TraceQueryStartData{SQL:"fast"})
 enabled.TraceQueryEnd(fast,nil,pgx.TraceQueryEndData{})
 enabled.TraceQueryEnd(slow,nil,pgx.TraceQueryEndData{Err:errors.New("query failed")})
 enabled.Record("test",17*time.Nanosecond,false);enabled.recordHTTP(23*time.Nanosecond)
 got:=enabled.takeTimings()
 if len(got.QueryNs)!=2||got.QueryNs[1]<int64(2*time.Millisecond)||got.QueryNs[0]>=got.QueryNs[1]||got.QueryErrors!=1||len(got.HandlerNs)!=1||got.HandlerNs[0]!=17||len(got.HTTPNs)!=1||got.HTTPNs[0]!=23{t.Fatalf("wrong timings: %+v",got)}
 enabled.Record("test",29*time.Nanosecond,false)
 next:=enabled.takeTimings()
 if len(next.QueryNs)!=0||next.QueryErrors!=0||len(next.HTTPNs)!=0||len(next.HandlerNs)!=1||next.HandlerNs[0]!=29||got.HandlerNs[0]!=17{t.Fatal("timing snapshot reused or not drained")}
}
func TestConcurrentCaptureTimings(t *testing.T){
 c:=capture{diagnostic:true};var group sync.WaitGroup
 for range 8{group.Go(func(){for range 100{ctx:=c.TraceQueryStart(context.Background(),nil,pgx.TraceQueryStartData{SQL:"SELECT 1"});c.TraceQueryEnd(ctx,nil,pgx.TraceQueryEndData{});c.Record("test",1,false);c.recordHTTP(2)}})}
 group.Wait();got:=c.takeTimings()
 if len(got.QueryNs)!=800||len(got.HandlerNs)!=800||len(got.HTTPNs)!=800||got.QueryErrors!=0{t.Fatalf("lost timings: %+v",got)}
}
`
