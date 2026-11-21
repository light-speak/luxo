package main

import (
	_ "embed"
	"fmt"
)

//go:embed relations.luxo
var fixtureRelationshipSchema string

func generateRelationshipSQLFixture(out string) error {
	const setup = `CREATE TABLE persons(id bigint PRIMARY KEY,name text,private_note text);
INSERT INTO persons VALUES(0,'zero',repeat('p',4096)),(1,'alice',repeat('p',4096)),(2,'bob',repeat('p',4096)),(3,'empty',repeat('p',4096));
CREATE TABLE articles(id bigint PRIMARY KEY,title text,body text,author_id bigint);
INSERT INTO articles SELECT 9007199254740992+i,'article-'||i,repeat('b',4096),CASE i WHEN 1 THEN 1 WHEN 2 THEN 1 WHEN 3 THEN 2 WHEN 4 THEN NULL WHEN 5 THEN 0 ELSE 999 END FROM generate_series(1,6) i`
	const initialize = `package main
import "github.com/light-speak/luxo/pkg/lux/dataloader"
func configureSQLApp(app *App) {
 app.SetLoaders(NewDefaultLoaders(app, dataloader.Config{}))
}
`
	return generateSQLFixtureWithSetup(out, fixtureRelationshipSchema, setup, []byte(initialize))
}

func generateRelationshipSQLMode(out string, arguments []string, modes ...bool) error {
	if len(arguments) != 0 {
		return fmt.Errorf("-sql-relations only accepts -out")
	}
	for _, enabled := range modes {
		if enabled {
			return fmt.Errorf("-sql-relations only accepts -out")
		}
	}
	return generateRelationshipSQLFixture(out)
}
