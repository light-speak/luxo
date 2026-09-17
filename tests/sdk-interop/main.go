// Command sdk-interop generates a real Go service for cross-SDK protocol tests.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

//go:embed schema.luxo
var fixtureSchema string

//go:embed server.go.txt
var fixtureServer []byte

func main() {
	out := flag.String("out", "", "existing empty directory under the core repository's .tmp")
	run := flag.Bool("run", false, "run a consumer command against an owned temporary Go service")
	gateway := flag.Bool("gateway", false, "run the consumer through a generated gateway and a separate TCP RPC service")
	flag.Parse()
	var err error
	if *run && *out != "" {
		err = fmt.Errorf("-run and -out cannot be combined")
	} else if *gateway && !*run {
		err = fmt.Errorf("-gateway requires -run")
	} else if *run {
		err = runFixtureCommand(flag.Args(), *gateway)
	} else {
		err = generateFixture(*out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generateFixture(out string) error {
	if err := validateOutput(out); err != nil {
		return err
	}
	files, err := compileFixture(fixtureSchema)
	if err != nil {
		return err
	}
	files["main.go"] = fixtureServer
	files["deployment.go"] = fixtureDeploymentServer
	for name, source := range files {
		file, err := os.OpenFile(filepath.Join(out, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(source)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func validateOutput(out string) error {
	if out == "" {
		return fmt.Errorf("-out is required")
	}
	root, err := filepath.Abs(".tmp")
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(out)
	if err != nil {
		return err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output must be a dedicated directory inside %s", root)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory is not empty: %s", out)
	}
	return nil
}

func compileFixture(source string) (map[string][]byte, error) {
	tokens, lexErrors := lexer.New(source, "origin/precision/schema.luxo").Tokenize()
	if len(lexErrors) > 0 {
		return nil, lexErrors[0]
	}
	file, parseErrors := parser.New(tokens).Parse("origin/precision/schema.luxo")
	if len(parseErrors) > 0 {
		return nil, parseErrors[0]
	}
	result := semantic.AnalyzeModules([]*ast.File{file})
	if len(result.Errors) > 0 {
		return nil, result.Errors[0]
	}
	lock := lockfile.New()
	lock.Update(result.Files)
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{
		IDs: fixtureIDs(lock), Events: codegen.BuildEventContext(result.Files, ""),
	})
	if err != nil {
		return nil, err
	}
	generated, err := generator.Generate(result, "main", nil)
	if err != nil {
		return nil, err
	}
	entry, err := codegen.GenerateGatewayEntryChecked(result, "sdk-interop")
	if err != nil {
		return nil, err
	}
	proxies, err := fixtureGatewayProxies(entry)
	if err != nil {
		return nil, err
	}
	generated.Files["gateway_proxy.gen.go"] = proxies
	return generated.Files, nil
}

func fixtureIDs(lock *lockfile.LockFile) codegen.StableIDs {
	ids := codegen.StableIDs{
		ModelFields: make(map[string]map[string]int), APIs: make(map[string]int),
		APIParams: make(map[string]map[string]int), APIParamTypes: make(map[string]map[string]string),
	}
	for _, declarations := range []map[string]*lockfile.ModelLock{lock.Models, lock.Types} {
		for name, model := range declarations {
			ids.ModelFields[name] = model.Fields
		}
	}
	for name, api := range lock.APIs {
		ids.APIs[name] = api.ID
		ids.APIParams[name] = api.Params
		ids.APIParamTypes[name] = api.ParamTypes
	}
	return ids
}
