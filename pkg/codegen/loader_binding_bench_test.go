package codegen

import (
	"fmt"
	"strings"
	"testing"
)

// Exercise the emitted binding code independently of database/network variance.
// Pointer slots retain the generated representation without invoking loaders.
func TestGeneratedLoaderBindingWidths(t *testing.T) {
	for _, count := range []int{1, 16, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			source, assertions := loaderBindingFixture(count)
			runGeneratedProtocolPackage(t, source, assertions)
		})
	}
}

func loaderBindingFixture(count int) (string, string) {
	var source, assertions strings.Builder
	source.WriteString("package generated\ntype App struct{}\n")
	slots := make([]loaderBindingSlot, count)
	for i := range slots {
		slots[i] = loaderBindingSlot{fmt.Sprintf("Field%d", i), "*int"}
	}
	writeLoaderDeclarations(&source, slots)
	writeCustomLoaderTransactionFactory(&source)
	// Freeze the old value receiver instead of calling the evolving implementation.
	source.WriteString("func (l Loaders) referenceBinding() loaderBinding { return loaderBinding{\n")
	for _, slot := range slots {
		fmt.Fprintf(&source, "%s: l.%s,\n", slot.name, slot.name)
	}
	source.WriteString("} }\n")
	assertions.WriteString("package generated\nimport (\"testing\";\"unsafe\")\n")
	assertions.WriteString("func fixtureLoaders() Loaders { return Loaders{\n")
	for _, slot := range slots {
		fmt.Fprintf(&assertions, "%s: new(int),\n", slot.name)
	}
	assertions.WriteString("}.WithTransactionFactory(func(*App)Loaders{return Loaders{}}) }\n")
	assertions.WriteString("func TestExactBinding(t *testing.T) {\n l:=fixtureLoaders(); if l.transactionFactoryRequired(){t.Fatal(\"valid binding rejected\")}\n")
	for _, slot := range slots {
		fmt.Fprintf(&assertions, "{ changed:=l; changed.%s=new(int); if !changed.transactionFactoryRequired(){t.Fatal(%q)}; changed.%s=nil; if !changed.transactionFactoryRequired(){t.Fatal(%q)} }\n", slot.name, slot.name, slot.name, slot.name)
	}
	assertions.WriteString("}\n")
	assertions.WriteString(loaderBindingBenchmarks)
	return source.String(), assertions.String()
}

const loaderBindingBenchmarks = `
var bindingResult bool
var bindingValue Loaders

// Same-call-site control retains the first, value-receiver snapshot implementation.
func (l Loaders) referenceRequired() bool {
  if l.rebind!=nil {return l.bound!=l.referenceBinding()}
  return l.referenceBinding()!=(loaderBinding{})
}

func BenchmarkLoaderBinding(b *testing.B) {
  for _,bound:=range []bool{false,true} {
    name:="empty"; if bound {name="bound"}
    b.Run(name,func(b *testing.B){
      original:=Loaders{}; if bound {original=fixtureLoaders()}
      for _,reference:=range []bool{true,false} {
        label:="current"; if reference {label="value-reference"}
        b.Run(label,func(b *testing.B){
          value:=original
          check:=(*Loaders).transactionFactoryRequired
          if reference {check=(*Loaders).referenceRequired}
          b.ReportAllocs()
          for b.Loop(){ bindingResult=check(&value) }
          if bindingResult {b.Fatal("unexpected invalid binding")}
          b.ReportMetric(float64(unsafe.Sizeof(value)),"type-B")
        })
      }
    })
  }
  b.Run("bind",func(b *testing.B){
    value:=fixtureLoaders(); factory:=value.rebind
    b.ReportAllocs()
    for b.Loop(){bindingValue=value.WithTransactionFactory(factory)}
    if bindingValue.transactionFactoryRequired(){b.Fatal("binding rejected")}
  })
}
`
