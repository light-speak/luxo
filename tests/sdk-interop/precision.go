package main

import (
	_ "embed"
	"fmt"
)

//go:embed precision.luxo
var fixturePrecisionSchema string

func generatePrecisionFixture(out string) error {
	const setup = "CREATE TABLE precision_records(id bigint PRIMARY KEY,whole bigint NOT NULL,ratio double precision NOT NULL,amount numeric NOT NULL,moment timestamptz NOT NULL,elapsed interval NOT NULL,token uuid NOT NULL); CREATE TABLE nullable_records(id bigint PRIMARY KEY,number bigint,items bigint[])"
	return generateSQLFixtureWithSetup(out, fixturePrecisionSchema, setup, nil)
}

func generatePrecisionMode(out string, modes ...bool) error {
	for _, enabled := range modes {
		if enabled {
			return fmt.Errorf("-sql-precision only accepts -out")
		}
	}
	return generatePrecisionFixture(out)
}
