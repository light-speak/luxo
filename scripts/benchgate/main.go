package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	confirmationFlag  = "--requires-confirmation"
	maxTimeRegression = 5.0
	minimumSamples    = 10
)

type evaluation struct {
	TimeComparisons       int
	AllocationComparisons int
	Comparisons           map[comparisonKey]struct{}
	Measurements          map[comparisonKey]measurement
	Regressions           []regression
	Unconfirmed           []regression
}

type comparisonKey struct {
	Package   string
	Benchmark string
	Unit      string
}

type regression struct {
	Key         comparisonKey
	Description string
}

type measurement struct {
	Base float64
	Head float64
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
	options, paths, err := parseGateOptions(args)
	if err != nil {
		return err
	}
	if options.confirmation {
		if len(paths) != 1 {
			return usageError()
		}
		return reportConfirmationRequirement(paths[0], output)
	}
	switch len(paths) {
	case 1:
		return evaluatePrimary(paths[0], output)
	case 2:
		return evaluateConfirmation(paths[0], paths[1], output, options.costs)
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: benchgate [--requires-confirmation] [--approved-costs policy.json --base-sha SHA] <primary.csv> [confirmation.csv]")
}

func reportConfirmationRequirement(path string, output io.Writer) error {
	result, err := evaluateFile(path)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, len(result.Regressions) > 0)
	return err
}

func evaluatePrimary(path string, output io.Writer) error {
	result, err := evaluateFile(path)
	if err != nil {
		return err
	}
	for _, regression := range result.Regressions {
		fmt.Fprintf(output, "candidate performance regression: %s\n", regression.Description)
	}
	if len(result.Regressions) > 0 {
		return fmt.Errorf("performance gate requires confirmation for %d regression candidate(s)", len(result.Regressions))
	}
	fmt.Fprintf(output, "performance gate passed: %d time and %d allocation comparisons; primary sample group clean without confirmation\n", result.TimeComparisons, result.AllocationComparisons)
	return nil
}

func evaluateConfirmation(primaryPath, confirmationPath string, output io.Writer, costs map[costKey]approvedCost) error {
	primary, err := evaluateFile(primaryPath)
	if err != nil {
		return err
	}
	confirmation, err := evaluateFile(confirmationPath)
	if err != nil {
		return err
	}
	result, err := confirmEvaluations(primary, confirmation)
	if err != nil {
		return err
	}
	if err := applyApprovedCosts(&result, primary, confirmation, costs, output); err != nil {
		return err
	}
	for _, unconfirmed := range result.Unconfirmed {
		fmt.Fprintf(output, "unconfirmed performance variation: %s\n", unconfirmed.Description)
	}
	for _, regression := range result.Regressions {
		fmt.Fprintf(output, "confirmed performance regression: %s\n", regression.Description)
	}
	if len(result.Regressions) > 0 {
		return fmt.Errorf("performance gate rejected %d regression(s)", len(result.Regressions))
	}
	fmt.Fprintf(output, "performance gate passed: %d time and %d allocation comparisons confirmed across two sample groups\n", result.TimeComparisons, result.AllocationComparisons)
	return nil
}

func evaluateFile(path string) (evaluation, error) {
	file, err := os.Open(path)
	if err != nil {
		return evaluation{}, err
	}
	defer file.Close()
	return evaluate(file)
}

func evaluate(input io.Reader) (evaluation, error) {
	reader := csv.NewReader(input)
	reader.FieldsPerRecord = -1
	evaluator := comparisonEvaluator{result: evaluation{
		Comparisons:  make(map[comparisonKey]struct{}),
		Measurements: make(map[comparisonKey]measurement),
	}}
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
	sortRegressions(evaluator.result.Regressions)
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
	key := comparisonKey{Package: evaluator.packageName, Benchmark: row[0], Unit: evaluator.unit}
	if _, exists := evaluator.result.Comparisons[key]; exists {
		return fmt.Errorf("duplicate benchmark comparison: %v", key)
	}
	values, err := parseMeasurement(row[1], row[3])
	if err != nil {
		return fmt.Errorf("%s/%s: %w", evaluator.packageName, row[0], err)
	}
	evaluator.result.Comparisons[key] = struct{}{}
	evaluator.result.Measurements[key] = values
	if significant && change > threshold {
		evaluator.result.Regressions = append(
			evaluator.result.Regressions,
			regression{
				Key:         key,
				Description: fmt.Sprintf("%s/%s %s %s (base=%g head=%g delta=%+g)", evaluator.packageName, row[0], evaluator.unit, row[5], values.Base, values.Head, values.Head-values.Base),
			},
		)
	}
	return nil
}

func confirmEvaluations(primary, confirmation evaluation) (evaluation, error) {
	if !matchingComparisons(primary.Comparisons, confirmation.Comparisons) {
		return evaluation{}, errors.New("benchmark comparison sets differ between sample groups")
	}
	result := evaluation{
		TimeComparisons:       primary.TimeComparisons,
		AllocationComparisons: primary.AllocationComparisons,
		Comparisons:           primary.Comparisons,
	}
	primaryByKey := regressionsByKey(primary.Regressions)
	confirmationByKey := regressionsByKey(confirmation.Regressions)
	for key, first := range primaryByKey {
		second, confirmed := confirmationByKey[key]
		if confirmed {
			first.Description += "; confirmation " + second.Description
			result.Regressions = append(result.Regressions, first)
			continue
		}
		result.Unconfirmed = append(result.Unconfirmed, first)
	}
	for key, second := range confirmationByKey {
		if _, seen := primaryByKey[key]; !seen {
			result.Unconfirmed = append(result.Unconfirmed, second)
		}
	}
	sortRegressions(result.Regressions)
	sortRegressions(result.Unconfirmed)
	return result, nil
}

func matchingComparisons(first, second map[comparisonKey]struct{}) bool {
	if len(first) != len(second) {
		return false
	}
	for key := range first {
		if _, exists := second[key]; !exists {
			return false
		}
	}
	return true
}

func regressionsByKey(regressions []regression) map[comparisonKey]regression {
	byKey := make(map[comparisonKey]regression, len(regressions))
	for _, item := range regressions {
		byKey[item.Key] = item
	}
	return byKey
}

func sortRegressions(regressions []regression) {
	sort.Slice(regressions, func(i, j int) bool {
		return regressions[i].Description < regressions[j].Description
	})
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
	if err != nil || math.IsNaN(change) || math.IsInf(change, 0) {
		return 0, false, fmt.Errorf("invalid delta %q", value)
	}
	return change, true, nil
}

func parseMeasurement(base, head string) (measurement, error) {
	var result measurement
	for index, value := range []string{base, head} {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
			return measurement{}, fmt.Errorf("invalid benchmark measurement %q", value)
		}
		if index == 0 {
			result.Base = parsed
		} else {
			result.Head = parsed
		}
	}
	return result, nil
}
