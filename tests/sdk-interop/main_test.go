package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateFixtureOwnsOnlyEmptyProjectScratch(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(root, "sdk-interop-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(out) != root {
			t.Fatal("refusing cleanup outside scratch")
		}
		if err := os.RemoveAll(out); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{"", ".", root, filepath.Join(out, "missing")} {
		if err := generateFixture(path); err == nil {
			t.Errorf("accepted output %q", path)
		}
	}
	if err := generateFixture(out); err != nil {
		t.Fatal(err)
	}
	if err := generateFixture(out); err == nil {
		t.Fatal("overwrote generated files")
	}
	if data, err := os.ReadFile(filepath.Join(out, "main.go")); err != nil || !bytes.Equal(data, fixtureServer) {
		t.Fatalf("server template = %q, %v", data, err)
	}
}

func TestCompileFixture(t *testing.T) {
	files, err := compileFixture(fixtureSchema)
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range files {
		if _, err := parser.ParseFile(token.NewFileSet(), name, source, parser.AllErrors); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"model.gen.go", "app.gen.go", "handler.gen.go", "schema.gen.go", "stream.gen.go", "gateway_proxy.gen.go"} {
		if len(files[name]) == 0 {
			t.Errorf("missing generated %s", name)
		}
	}
	for _, name := range []string{"EchoPrecision", "RejectJSON"} {
		if bytes.Contains(files["native.gen.go"], []byte(name)) {
			t.Fatalf("%s must run a compiled Luxo body", name)
		}
	}
	if !bytes.Contains(files["handler.gen.go"], []byte("WriteColumnarPrecision")) {
		t.Fatal("list response must use the generated columnar writer")
	}
	if bytes.Contains(files["handler.gen.go"], []byte(`Type: "Model", TypeName: "FixtureState"`)) {
		t.Fatal("generated handlers must agree with schema enum parameter metadata")
	}
}

func TestFixtureGatewayRequiresActualGeneratedProxies(t *testing.T) {
	for _, source := range []string{
		"package main\nfunc main() {}",
		"package main\nfunc proxyHandler() {}",
		"package main\nfunc rpcStreamProxy() {}",
		"not Go",
	} {
		if _, err := fixtureGatewayProxies([]byte(source)); err == nil {
			t.Errorf("accepted missing or invalid generated proxies: %s", source)
		}
	}
	files, err := compileFixture(fixtureSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"func proxyHandler(", "func rpcStreamProxy(", "client.CallWithMaskTargetContext(", "client.SubscribeContext("} {
		if !bytes.Contains(files["gateway_proxy.gen.go"], []byte(fragment)) {
			t.Errorf("missing real generated proxy: %s", fragment)
		}
	}
	if bytes.Contains(files["gateway_proxy.gen.go"], []byte("func main(")) {
		t.Fatal("fixture must own process startup, not run the deployment entry point")
	}
}

func TestFixtureGatewayFollowsGeneratedHelpers(t *testing.T) {
	source := []byte(`package main
import "fmt"
import "net"
func proxyHandler() { proxyFirst(); proxyFirst() }
func proxyFirst() { proxySecond() }
func proxySecond() { fmt.Print("helper"); proxyFirst() }
func rpcStreamProxy() {}
func unused() { net.Listen("tcp", "") }
func main() { unused() }
`)
	generated, err := fixtureGatewayProxies(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, included := range []string{"func proxyFirst(", "func proxySecond(", `"fmt"`} {
		if !bytes.Contains(generated, []byte(included)) {
			t.Errorf("missing reachable helper dependency %q", included)
		}
	}
	for _, excluded := range []string{"func main(", "func unused(", `"net"`} {
		if bytes.Contains(generated, []byte(excluded)) {
			t.Errorf("included unrelated deployment code %q", excluded)
		}
	}
}

func TestCompileFixtureRejectsInvalidSource(t *testing.T) {
	for _, source := range []string{
		"`", "type Broken {", "api broken(value: Missing): Missing { return value }",
	} {
		if _, err := compileFixture(source); err == nil {
			t.Errorf("accepted invalid source %q", source)
		}
	}
}
