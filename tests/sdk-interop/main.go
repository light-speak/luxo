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
	futureEnum := flag.Bool("future-enum", false, "generate a newer enum revision for old-SDK interoperability tests; requires -out")
	enumUpgrade := flag.Bool("enum-upgrade", false, "run an old-Schema consumer against a newer enum service; requires -run")
	flag.Parse()
	var err error
	if *enumUpgrade && (!*run || *futureEnum) {
		err = fmt.Errorf("-enum-upgrade requires -run and cannot be combined with -future-enum")
	} else if *futureEnum && (*run || *out == "") {
		err = fmt.Errorf("-future-enum requires -out and cannot be combined with -run")
	} else if *run && *out != "" {
		err = fmt.Errorf("-run and -out cannot be combined")
	} else if *gateway && !*run {
		err = fmt.Errorf("-gateway requires -run")
	} else if *enumUpgrade {
		err = runEnumUpgradeCommand(flag.Args(), *gateway)
	} else if *run {
		err = runFixtureCommand(flag.Args(), *gateway)
	} else {
		err = generateFixtureRevision(*out, *futureEnum)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generateFixture(out string) error {
	return generateFixtureRevision(out, false)
}

func generateFixtureRevision(out string, futureEnum bool) error {
	if err := validateOutput(out); err != nil {
		return err
	}
	source, server := fixtureSchema, fixtureServer
	if futureEnum {
		var err error
		source, server, err = fixtureEnumUpgrade(source, server)
		if err != nil {
			return err
		}
	}
	baseline := ""
	if futureEnum {
		baseline = fixtureSchema
	}
	files, err := compileFixtureRevision(source, baseline)
	if err != nil {
		return err
	}
	files["main.go"] = server
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

// Compile two actual schemas, rather than masking the live server's metadata.
// Refuse template drift so the upgrade test cannot silently test one revision.
func fixtureEnumUpgrade(source string, server []byte) (string, []byte, error) {
	const original = "enum FixtureState {\n  OPEN\n  CLOSED\n}"
	const upgraded = "enum FixtureState {\n  OPEN\n  CLOSED\n  FUTURE\n}"
	if strings.Count(source, original) != 1 {
		return "", nil, fmt.Errorf("missing unique enum upgrade anchor")
	}
	text := string(server)
	for _, pair := range [][2]string{
		{`FixtureState("OPEN")`, `FixtureState("FUTURE")`},
		{`stream.Send([]FixtureState{"OPEN", "CLOSED"})`, `stream.Send([]FixtureState{"OPEN", "FUTURE", "CLOSED"})`},
	} {
		if strings.Count(text, pair[0]) != 1 {
			return "", nil, fmt.Errorf("missing unique stream upgrade anchor: %s", pair[0])
		}
		text = strings.Replace(text, pair[0], pair[1], 1)
	}
	source, err := fixtureAdditiveUpgrade(strings.Replace(source, original, upgraded, 1))
	return source, []byte(text), err
}

func fixtureAdditiveUpgrade(source string) (string, error) {
	for _, pair := range [][2]string{
		{"api echoInteger(value: Int): Int {\n  return value\n}", "api echoInteger(value: Int, delta: Int = 0): Int {\n  return value + delta\n}"},
		{"model PageRecord {\n", "model PageRecord {\n  future: String?\n"},
	} {
		if strings.Count(source, pair[0]) != 1 {
			return "", fmt.Errorf("missing unique additive revision anchor: %s", pair[0])
		}
		source = strings.Replace(source, pair[0], pair[1], 1)
	}
	return "api aFutureInteger(value: Int): Int { return value }\n\n" + source, nil
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
	return compileFixtureRevision(source, "")
}

func analyzeFixture(source string) (*semantic.Result, error) {
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
	return result, nil
}

func compileFixtureRevision(source, baseline string) (map[string][]byte, error) {
	lock := lockfile.New()
	if baseline != "" {
		prior, err := analyzeFixture(baseline)
		if err != nil {
			return nil, err
		}
		if err := lock.Update(prior.Files); err != nil {
			return nil, err
		}
	}
	result, err := analyzeFixture(source)
	if err != nil {
		return nil, err
	}
	if err := lock.Update(result.Files); err != nil {
		return nil, err
	}
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
	entry, err := codegen.GenerateGatewayEntryChecked(result, "sdk-interop", codegen.EntryBackends{Events: "memory"})
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
		APIParams: make(map[string]map[string]int),
	}
	for _, declarations := range []map[string]*lockfile.ModelLock{lock.Models, lock.Types} {
		for name, model := range declarations {
			ids.ModelFields[name] = model.Fields
		}
	}
	for name, api := range lock.APIs {
		ids.APIs[name] = api.ID
		ids.APIParams[name] = api.Params
	}
	return ids
}
