package main

import (
	"errors"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const benchmarkPackagePrefix = "github.com/light-speak/luxo/"

func reportConfirmationTargets(path string, output io.Writer) error {
	primary, err := evaluateFile(path)
	if err != nil {
		return err
	}
	cases, err := confirmationCases(primary)
	if err != nil || len(cases) == 0 {
		return err
	}
	lines := make([]string, 0, len(cases))
	for key := range cases {
		lines = append(lines, strings.TrimPrefix(key.Package, benchmarkPackagePrefix)+"\tBenchmark"+key.Benchmark)
	}
	slices.Sort(lines)
	_, err = io.WriteString(output, strings.Join(lines, "\n")+"\n")
	return err
}

func confirmationCases(primary evaluation) (map[costKey]struct{}, error) {
	cases := make(map[costKey]struct{})
	for _, item := range primary.Regressions {
		key := costKey{Package: item.Key.Package, Benchmark: item.Key.Benchmark}
		if _, exists := cases[key]; exists {
			continue
		}
		if !validTargetPackage(key.Package) || !validTargetBenchmark(key.Benchmark) {
			return nil, fmt.Errorf("invalid confirmation target package %q or benchmark %q", key.Package, key.Benchmark)
		}
		cases[key] = struct{}{}
	}
	return cases, nil
}

func validTargetPackage(packageName string) bool {
	name, inModule := strings.CutPrefix(packageName, benchmarkPackagePrefix)
	return inModule && strings.HasPrefix(name, "pkg/") && fs.ValidPath(name) &&
		!strings.Contains(name, "..") && strings.IndexFunc(name, invalidPackageRune) == -1 &&
		benchlayout.Canonical(name) == name
}

func invalidPackageRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("/._-~+", r)
}

func validTargetBenchmark(name string) bool {
	// benchstat omits Benchmark but preserves the CPU suffix and sub-benchmark path.
	withoutCPU, cpuTwo := strings.CutSuffix(name, "-2")
	function, _, _ := strings.Cut(withoutCPU, "/")
	return cpuTwo && function != "" && token.IsIdentifier("Benchmark"+function) &&
		utf8.ValidString(name) && strings.IndexFunc(name, invalidTargetRune) == -1
}

func invalidTargetRune(r rune) bool {
	return !unicode.IsPrint(r) || unicode.IsSpace(r)
}

func confirmTargetedEvaluations(primary, confirmation evaluation) (evaluation, error) {
	cases, err := confirmationCases(primary)
	if err != nil {
		return evaluation{}, err
	}
	if len(cases) == 0 {
		return evaluation{}, errors.New("targeted confirmation requires primary regression candidates")
	}
	if primary.platform() == "" || primary.GOOS != confirmation.GOOS || primary.GOARCH != confirmation.GOARCH {
		return evaluation{}, errors.New("benchmark platforms are missing or differ between sample groups")
	}
	expected := make(map[comparisonKey]struct{}, len(cases)*len(comparisonUnits))
	for key := range cases {
		for _, unit := range comparisonUnits {
			expected[comparisonKey{Package: key.Package, Benchmark: key.Benchmark, Unit: unit}] = struct{}{}
		}
	}
	if !matchingComparisons(expected, confirmation.Comparisons) {
		return evaluation{}, errors.New("benchmark comparison sets differ from targeted confirmation requirements")
	}
	return combineConfirmation(primary, confirmation), nil
}
