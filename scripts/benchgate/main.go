package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	maxTimeRegression = 5.0
	minimumSamples    = 10
)

type evaluation struct {
	TimeComparisons       int
	AllocationComparisons int
	Regressions           []string
}

type comparisonEvaluator struct {
	packageName string
	unit        string
	result      evaluation
}

func main() {
	os.Exit(command(os.Args[1:], os.Stdout, os.Stderr))
}

func command(args []string, output, errorOutput io.Writer) int {
	if err := run(args, output); err != nil {
		fmt.Fprintln(errorOutput, err)
		return 1
	}
	return 0
}

func run(args []string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: benchgate <benchstat.csv>")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer file.Close()
	result, err := evaluate(file)
	if err != nil {
		return err
	}
	for _, regression := range result.Regressions {
		fmt.Fprintf(output, "performance regression: %s\n", regression)
	}
	if len(result.Regressions) > 0 {
		return fmt.Errorf("performance gate rejected %d regression(s)", len(result.Regressions))
	}
	fmt.Fprintf(output, "performance gate passed: %d time and %d allocation comparisons\n", result.TimeComparisons, result.AllocationComparisons)
	return nil
}

func evaluate(input io.Reader) (evaluation, error) {
	reader := csv.NewReader(input)
	reader.FieldsPerRecord = -1
	evaluator := comparisonEvaluator{}
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return evaluation{}, err
		}
		if err := evaluator.consume(row); err != nil {
			return evaluation{}, err
		}
	}
	if evaluator.result.TimeComparisons == 0 {
		return evaluation{}, errors.New("benchstat output contains no time comparisons")
	}
	if evaluator.result.AllocationComparisons == 0 {
		return evaluation{}, errors.New("benchstat output contains no allocation comparisons")
	}
	sort.Strings(evaluator.result.Regressions)
	return evaluator.result, nil
}

func (evaluator *comparisonEvaluator) consume(row []string) error {
	if len(row) == 1 && strings.HasPrefix(row[0], "pkg: ") {
		evaluator.packageName = strings.TrimPrefix(row[0], "pkg: ")
		evaluator.unit = ""
		return nil
	}
	if isUnitHeader(row) {
		evaluator.unit = row[1]
		return nil
	}
	threshold, supported := regressionThreshold(evaluator.unit)
	if !supported || !isComparisonRow(row) {
		return nil
	}
	if evaluator.packageName == "" {
		return errors.New("benchmark comparison has no package header")
	}
	if err := validateSamples(row[6]); err != nil {
		return fmt.Errorf("%s/%s: %w", evaluator.packageName, row[0], err)
	}
	change, significant, err := parseDelta(row[5])
	if err != nil {
		return fmt.Errorf("%s/%s: %w", evaluator.packageName, row[0], err)
	}
	evaluator.countComparison()
	if significant && change > threshold {
		evaluator.result.Regressions = append(
			evaluator.result.Regressions,
			fmt.Sprintf("%s/%s %s %s", evaluator.packageName, row[0], evaluator.unit, row[5]),
		)
	}
	return nil
}

func (evaluator *comparisonEvaluator) countComparison() {
	if evaluator.unit == "sec/op" {
		evaluator.result.TimeComparisons++
		return
	}
	evaluator.result.AllocationComparisons++
}

func isUnitHeader(row []string) bool {
	return len(row) >= 7 && row[0] == "" && row[2] == "CI" && row[4] == "CI" && row[5] == "vs base"
}

func isComparisonRow(row []string) bool {
	return len(row) >= 7 && row[0] != "" && row[0] != "geomean"
}

func regressionThreshold(unit string) (float64, bool) {
	switch unit {
	case "sec/op":
		return maxTimeRegression, true
	case "B/op", "allocs/op":
		return 0, true
	default:
		return 0, false
	}
}

func validateSamples(value string) error {
	index := strings.LastIndex(value, "n=")
	if index < 0 {
		return fmt.Errorf("invalid sample count %q", value)
	}
	counts := strings.Split(value[index+2:], "+")
	for _, count := range counts {
		parsed, err := strconv.Atoi(count)
		if err != nil {
			return fmt.Errorf("invalid sample count %q", value)
		}
		if parsed < minimumSamples {
			return fmt.Errorf("requires at least %d samples per revision, got %q", minimumSamples, value)
		}
	}
	return nil
}

func parseDelta(value string) (float64, bool, error) {
	if value == "~" {
		return 0, false, nil
	}
	if !strings.HasSuffix(value, "%") {
		return 0, false, fmt.Errorf("invalid delta %q", value)
	}
	change, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid delta %q", value)
	}
	return change, true, nil
}
