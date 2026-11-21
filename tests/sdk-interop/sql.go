package main

import (
	_ "embed"
	"fmt"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lockfile"
)

//go:embed sql.luxo
var fixtureSQLSchema string

//go:embed sql_server.go.txt
var fixtureSQLServer []byte

func generateSQLFixture(out string) error {
	const setup = "CREATE TABLE products(id bigint PRIMARY KEY,name text,price bigint,description text,secret bigint); INSERT INTO products SELECT 9007199254740992+i,'item-'||i,i,repeat('x',4096),i FROM generate_series(1,128) i"
	return generateSQLFixtureWithSetup(out, fixtureSQLSchema, setup, nil)
}

func generateSQLFixtureWithSetup(out, source, setup string, appSetup []byte) error {
	if err := validateOutput(out); err != nil {
		return err
	}
	result, err := analyzeFixture(source)
	if err != nil {
		return err
	}
	lock := lockfile.New()
	if err := lock.Update(result.Files); err != nil {
		return err
	}
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{Driver: codegen.DriverPG, IDs: fixtureIDs(lock)})
	if err != nil {
		return err
	}
	generated, err := generator.Generate(result, "main", nil)
	if err != nil {
		return err
	}
	entry, err := codegen.GenerateGatewayEntryChecked(result, "sdk-interop", codegen.EntryBackends{Events: "memory"})
	if err != nil {
		return err
	}
	proxies, err := fixtureGatewayProxies(entry, "proxyHandler")
	if err != nil {
		return err
	}
	generated.Files["gateway_proxy.gen.go"] = proxies
	generated.Files["main.go"] = fixtureSQLServer
	generated.Files["fixture_setup.go"] = fmt.Appendf(nil, "package main\nconst fixtureSetupSQL = %q\n", setup)
	if appSetup == nil {
		appSetup = []byte("package main\nfunc configureSQLApp(*App) {}\n")
	}
	generated.Files["fixture_app.go"] = appSetup
	return writeFixtureFiles(out, generated.Files)
}
