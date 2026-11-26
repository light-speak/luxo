package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

func writeConfirmationPlan(args []string) error {
	if len(args) != 5 {
		return errors.New("usage: benchplan --confirmation-plan <primary.json> <targets.tsv> <batch.tsv> <expected.json>")
	}
	primary, err := readExpectedSamples(args[1])
	if err != nil {
		return err
	}
	data, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	keys, err := confirmationSampleKeys(primary, string(data))
	if err != nil {
		return err
	}
	if err := os.WriteFile(args[3], []byte(formatConfirmationBatch(keys)), 0o644); err != nil {
		return err
	}
	file, err := os.Create(args[4])
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return errors.Join(encoder.Encode(expectedSamples{Base: keys, Head: keys}), file.Close())
}

func confirmationSampleKeys(primary expectedSamples, selection string) ([]sampleKey, error) {
	if selection == "" {
		return nil, errors.New("confirmation requires at least one selected benchmark")
	}
	common, err := pairedSampleKeys(primary)
	if err != nil {
		return nil, err
	}
	var keys []sampleKey
	seen := make(map[sampleKey]bool)
	for line := range strings.SplitSeq(strings.TrimSuffix(selection, "\n"), "\n") {
		pkg, name, ok := strings.Cut(line, "\t")
		key := sampleKey{Package: pkg, Name: name}
		if !ok || !validPackage(pkg) || !validConfirmationName(name) {
			return nil, fmt.Errorf("invalid confirmation target %q", line)
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate confirmation target: %s/%s", key.Package, key.Name)
		}
		if common[key] != 0b11 {
			return nil, fmt.Errorf("confirmation target lacks a paired primary measurement: %s/%s", key.Package, key.Name)
		}
		seen[key] = true
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right sampleKey) int {
		if order := strings.Compare(left.Package, right.Package); order != 0 {
			return order
		}
		return strings.Compare(left.Name, right.Name)
	})
	return keys, nil
}

func pairedSampleKeys(expected expectedSamples) (map[sampleKey]uint8, error) {
	common := make(map[sampleKey]uint8)
	for side, keys := range [][]sampleKey{expected.Base, expected.Head} {
		present := uint8(1 << side)
		for _, key := range keys {
			if common[key]&present != 0 {
				return nil, fmt.Errorf("duplicate primary sample: %s/%s", key.Package, key.Name)
			}
			common[key] |= present
		}
	}
	return common, nil
}

func validConfirmationName(name string) bool {
	path, cpu := strings.CutSuffix(name, "-2")
	root, _, _ := strings.Cut(path, "/")
	return cpu && strings.HasPrefix(root, "Benchmark") && root != "Benchmark" && token.IsIdentifier(root) &&
		strings.IndexFunc(path, unicode.IsSpace) < 0 && strings.IndexFunc(path, unicode.IsControl) < 0
}

func formatConfirmationBatch(keys []sampleKey) string {
	batches := make(map[benchmarkProfile][]string)
	for _, leaf := range keys {
		key := profileFor(leaf.Package, leaf.Name)
		var filter strings.Builder
		writeConfirmationFilter(&filter, strings.TrimSuffix(leaf.Name, "-2"))
		batches[key] = append(batches[key], filter.String())
	}
	return formatBatches(batches, "", "")
}

// The Go test matcher splits at unescaped slashes and top-level alternation.
// Anchor each literal component and join the fully qualified leaf patterns.
func writeConfirmationFilter(batch *strings.Builder, path string) {
	for i, part := range strings.Split(path, "/") {
		if i != 0 {
			batch.WriteByte('/')
		}
		batch.WriteByte('^')
		batch.WriteString(regexp.QuoteMeta(part))
		batch.WriteByte('$')
	}
}
