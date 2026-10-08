package codegen

import "testing"

func TestGeneratedNumericAggregates(t *testing.T) {
	runGeneratedFixture(t, `
model Metric @noTime { id: Int @id i: Int f: Float d: Decimal }
type Totals { integer: Int average: Float exact: Decimal }
fn totals(): Totals {
 val (integer, average, exact) = await {
  Metric.where(id > 0).sum { it.i }
  Metric.where(id > 0).avg { it.f }
  Metric.where(id > 0).sum { it.d }
 }
 return Totals { integer: integer, average: average, exact: exact }
}
fn intTotal(): Int { return Metric.where(id > 0).sum { row -> row.i } }
fn floatTotal(): Float { return Metric.where(id > 0).sum { it.f } }
fn decimalTotal(): Decimal { return Metric.where(id > 0).sum { it.d } }
fn aliasTotal(): Decimal { val query = Metric.where(id > 0) return query.sum(d) }
api summary(): Totals { return totals() }
`, `package generated
import (
 "testing"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestNumericAggregates(t *testing.T) {
 ctx:=t.Context()
 container,err:=postgres.Run(ctx,"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container)
 if err!=nil {t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil {t.Fatal(err)}
 db,err:=pg.NewDB(ctx,connection);if err!=nil {t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE metrics(id bigint PRIMARY KEY,i bigint,f double precision,d numeric); INSERT INTO metrics VALUES(1,9007199254740993,1.25,12345678901234567890.123456789),(2,2,2.5,0.000000001)");err!=nil {t.Fatal(err)}
 app:=NewFromDB(db)
 got,err:=app.totals(ctx)
 if err!=nil||got.Integer!=9007199254740995||got.Average!=1.875||got.Exact.String()!="12345678901234567890.12345679" {t.Fatal(got,err)}
 if value,err:=app.intTotal(ctx);err!=nil||value!=got.Integer {t.Fatal(value,err)}
 if value,err:=app.floatTotal(ctx);err!=nil||value!=3.75 {t.Fatal(value,err)}
 if value,err:=app.decimalTotal(ctx);err!=nil||!value.Equal(got.Exact) {t.Fatal(value,err)}
 if value,err:=app.aliasTotal(ctx);err!=nil||!value.Equal(got.Exact) {t.Fatal(value,err)}
}
`, StableIDs{APIs: map[string]int{"summary": 1}, ModelFields: map[string]map[string]int{
		"Metric": {"id": 1, "i": 2, "f": 3, "d": 4}, "Totals": {"integer": 1, "average": 2, "exact": 3},
	}})
}
