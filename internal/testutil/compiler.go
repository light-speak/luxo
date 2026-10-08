package testutil

import _ "embed"

//go:embed testdata/compiler-demo-v1.luxo
var compilerDemoV1 string

// CompilerDemoV1 returns the immutable historical compiler benchmark corpus.
// It includes semantic errors; it is not an example of a valid current application.
func CompilerDemoV1() string { return compilerDemoV1 }
