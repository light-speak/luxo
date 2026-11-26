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
	confirmationFlag         = "--requires-confirmation"
	confirmationTargetsFlag  = "--confirmation-targets"
	targetedConfirmationFlag = "--targeted-confirmation"
	maxTimeRegression        = 5.0
	minimumSamples           = 10
)

var comparisonUnits = [...]string{"sec/op", "B/op", "allocs/op"}

type evaluation struct {
	GOOS                  string
	GOARCH                string
	CPU                   string
	TimeComparisons       int
	AllocationComparisons int
	Comparisons           map[comparisonKey]struct{}
	Measurements          map[comparisonKey]measurement
	Regressions           []regression
	Unconfirmed           []regression
	TimingAdvisories      []regression
	invalidCPU            bool
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
	if options.confirmation || options.confirmationTargets {
		if len(paths) != 1 {
			return usageError()
		}
		if options.confirmationTargets {
			return reportConfirmationTargets(paths[0], output, options.advisoryTiming)
		}
		return reportConfirmationRequirement(paths[0], output, options.advisoryTiming)
	}
	if options.targetedConfirmation && len(paths) != 2 {
		return usageError()
	}
	switch len(paths) {
	case 1:
		return evaluatePrimary(paths[0], output, options.advisoryTiming)
	case 2:
		return evaluateConfirmation(paths[0], paths[1], output, options)
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: benchgate [--requires-confirmation | --confirmation-targets | --targeted-confirmation] [--approved-costs policy.json --base-sha SHA] <primary.csv> [confirmation.csv]")
}

func reportConfirmationRequirement(path string, output io.Writer, advisoryTiming bool) error {
	result, err := evaluateFile(path, advisoryTiming)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, len(result.Regressions) > 0)
	return err
}

func evaluatePrimary(path string, output io.Writer, advisoryTiming bool) error {
	result, err := evaluateFile(path, advisoryTiming)
	if err != nil {
		return err
	}
	if err := reportTimingAdvisories(output, "primary", result.TimingAdvisories); err != nil {
		return err
	}
	for _, regression := range result.Regressions {
		fmt.Fprintf(output, "candidate performance regression: %s\n", regression.Description)
	}
	if len(result.Regressions) > 0 {
		return fmt.Errorf("performance gate requires confirmation for %d regression candidate(s)", len(result.Regressions))
	}
	if advisoryTiming {
		_, err := fmt.Fprintf(output, "performance gate passed: %d advisory time and %d allocation comparisons; no allocation confirmation required\n", result.TimeComparisons, result.AllocationComparisons)
		return err
	}
	fmt.Fprintf(output, "performance gate passed: %d time and %d allocation comparisons; primary sample group clean without confirmation\n", result.TimeComparisons, result.AllocationComparisons)
	return nil
}

func evaluateConfirmation(primaryPath, confirmationPath string, output io.Writer, options gateOptions) error {
	primary, err := evaluateFile(primaryPath, options.advisoryTiming)
	if err != nil {
		return err
	}
	confirmation, err := evaluateFile(confirmationPath, options.advisoryTiming)
	if err != nil {
		return err
	}
	if options.advisoryTiming && primary.CPU != confirmation.CPU {
		return errors.New("benchmark CPUs differ between sample groups")
	}
	confirm := confirmEvaluations
	if options.targetedConfirmation {
		confirm = confirmTargetedEvaluations
	}
	result, err := confirm(primary, confirmation)
	if err != nil {
		return err
	}
	if err := applyApprovedCosts(&result, primary, confirmation, options.costs, output); err != nil {
		return err
	}
	if err := reportTimingAdvisories(output, "primary", primary.TimingAdvisories); err != nil {
		return err
	}
	if err := reportTimingAdvisories(output, "confirmation", confirmation.TimingAdvisories); err != nil {
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
	if options.advisoryTiming {
		_, err := fmt.Fprintf(output, "performance gate passed: primary %d advisory time and %d allocation comparisons; confirmation %d advisory time and %d allocation comparisons\n", result.TimeComparisons, result.AllocationComparisons, confirmation.TimeComparisons, confirmation.AllocationComparisons)
		return err
	}
	if options.targetedConfirmation {
		_, err := fmt.Fprintf(output, "performance gate passed: primary %d time and %d allocation comparisons; targeted confirmation %d time and %d allocation comparisons\n", result.TimeComparisons, result.AllocationComparisons, confirmation.TimeComparisons, confirmation.AllocationComparisons)
		return err
	}
	fmt.Fprintf(output, "performance gate passed: %d time and %d allocation comparisons confirmed across two sample groups\n", result.TimeComparisons, result.AllocationComparisons)
	return nil
}

func evaluateFile(path string, advisoryTiming bool) (evaluation, error) {
	file, err := os.Open(path)
	if err != nil {
		return evaluation{}, err
	}
	defer file.Close()
	result, err := evaluate(file)
	if err != nil || !advisoryTiming {
		return result, err
	}
	if err := result.applyAdvisoryTiming(); err != nil {
		return evaluation{}, err
	}
	return result, nil
}

func (result *evaluation) applyAdvisoryTiming() error {
	if result.platform() == "" {
		return errors.New("benchmark platform metadata is missing")
	}
	if result.CPU == "" || result.invalidCPU {
		return errors.New("missing or mixed benchmark CPU metadata")
	}
	for key := range result.Comparisons {
		if key.Unit == "sec/op" && (!validTargetPackage(key.Package) || !validTargetBenchmark(key.Benchmark)) {
			return fmt.Errorf("invalid advisory benchmark package %q or benchmark %q", key.Package, key.Benchmark)
		}
	}
	required := result.Regressions[:0]
	for _, item := range result.Regressions {
		if item.Key.Unit == "sec/op" {
			result.TimingAdvisories = append(result.TimingAdvisories, item)
			continue
		}
		required = append(required, item)
	}
	result.Regressions = required
	return nil
}

func reportTimingAdvisories(output io.Writer, group string, regressions []regression) error {
	for _, item := range regressions {
		if _, err := fmt.Fprintf(output, "advisory timing regression (%s): %s\n", group, item.Description); err != nil {
			return err
		}
	}
	return nil
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
	if err := validateComparisonMetrics(evaluator.result.Comparisons); err != nil {
		return evaluation{}, err
	}
	sortRegressions(evaluator.result.Regressions)
	return evaluator.result, nil
}

func validateComparisonMetrics(comparisons map[comparisonKey]struct{}) error {
	for key := range comparisons {
		for _, unit := range comparisonUnits {
			required := key
			required.Unit = unit
			if _, exists := comparisons[required]; !exists {
				return fmt.Errorf("%s/%s: missing paired %s comparison", key.Package, key.Benchmark, unit)
			}
		}
	}
	return nil
}

func (evaluator *comparisonEvaluator) consume(row []string) error {
	if handled, err := evaluator.consumePlatform(row); handled {
		return err
	}
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
	values, err := parseMeasurement(row[1], row[3])
	if err != nil {
		return fmt.Errorf("%s/%s: %w", evaluator.packageName, row[0], err)
	}
	change, significant, err := parseMeasuredDelta(row[5], values)
	if err != nil {
		return fmt.Errorf("%s/%s: %w", evaluator.packageName, row[0], err)
	}
	evaluator.countComparison()
	key := comparisonKey{Package: evaluator.packageName, Benchmark: row[0], Unit: evaluator.unit}
	if _, exists := evaluator.result.Comparisons[key]; exists {
		return fmt.Errorf("duplicate benchmark comparison: %v", key)
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
	if primary.GOOS != confirmation.GOOS || primary.GOARCH != confirmation.GOARCH {
		return evaluation{}, errors.New("benchmark platforms differ between sample groups")
	}
	if !matchingComparisons(primary.Comparisons, confirmation.Comparisons) {
		return evaluation{}, errors.New("benchmark comparison sets differ between sample groups")
	}
	return combineConfirmation(primary, confirmation), nil
}

func combineConfirmation(primary, confirmation evaluation) evaluation {
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
	return result
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

func parseMeasuredDelta(delta string, values measurement) (float64, bool, error) {
	// benchstat reports a significant change from zero as "?", not a percentage.
	// Preserve its significance decision; a finite positive result exceeds every
	// relative threshold. Invalid measurements are rejected before this step.
	if delta == "?" && values.Base == 0 && values.Head > 0 {
		return math.Inf(1), true, nil
	}
	return parseDelta(delta)
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

func (result evaluation) platform() string {
	if result.GOOS == "" || result.GOARCH == "" {
		return ""
	}
	return result.GOOS + "/" + result.GOARCH
}

func (evaluator *comparisonEvaluator) consumePlatform(row []string) (bool, error) {
	if len(row) != 1 {
		return false, nil
	}
	if value, ok := strings.CutPrefix(row[0], "cpu:"); ok {
		cpu := strings.TrimSpace(value)
		result := &evaluator.result
		if cpu == "" || strings.ContainsAny(cpu, "\t\r\n") || result.CPU != "" && result.CPU != cpu {
			result.invalidCPU = true
		}
		result.CPU = cpu
		return true, nil
	}
	for _, field := range []struct {
		prefix string
		target *string
	}{
		{"goos: ", &evaluator.result.GOOS}, {"goarch: ", &evaluator.result.GOARCH},
	} {
		if !strings.HasPrefix(row[0], field.prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(row[0], field.prefix))
		if value == "" || *field.target != "" && *field.target != value {
			return true, errors.New("missing or mixed benchmark platform metadata")
		}
		*field.target = value
		return true, nil
	}
	return false, nil
}
