package main

import (
	_ "embed"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lockfile"
)

//go:embed sql.luxo
var fixtureSQLSchema string

//go:embed sql_server.go.txt
var fixtureSQLServer []byte

func generateSQLFixture(out string) error {
	if err := validateOutput(out); err != nil {
		return err
	}
	result, err := analyzeFixture(fixtureSQLSchema)
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
	generated.Files["main.go"] = fixtureSQLServer
	return writeFixtureFiles(out, generated.Files)
}
