package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type sampleKey struct {
	Package string
	Name    string
}

type expectedSamples struct {
	Base []sampleKey
	Head []sampleKey
}

func expectedForShard(shard benchShard) expectedSamples {
	var expected expectedSamples
	for _, group := range shard.Groups {
		for _, name := range group.BaseNames {
			expected.Base = append(expected.Base, sampleKey{group.Package, name})
		}
		for _, name := range group.HeadNames {
			expected.Head = append(expected.Head, sampleKey{group.Package, name})
		}
	}
	return expected
}

func verifyFiles(args []string) error {
	if len(args) != 4 {
		return errors.New("usage: benchplan --verify <expected.json> <base.txt> <head.txt>")
	}
	expected, err := readExpectedSamples(args[1])
	if err != nil {
		return err
	}
	for i, keys := range [][]sampleKey{expected.Base, expected.Head} {
		if len(keys) == 0 {
			return errors.New("each shard requires base and head evidence")
		}
		if err := verifySampleFile(args[i+2], keys); err != nil {
			return err
		}
	}
	return nil
}

func readExpectedSamples(path string) (expectedSamples, error) {
	var expected expectedSamples
	data, err := os.ReadFile(path)
	if err != nil {
		return expected, err
	}
	err = json.Unmarshal(data, &expected)
	return expected, err
}

func verifySampleFile(path string, expected []sampleKey) error {
	return verifySampleFileObserving(path, expected, nil)
}

func verifySampleFileObserving(path string, expected []sampleKey, observe func(string) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := verifySamplesObserving(file, expected, observe); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func verifySamples(input io.Reader, expected []sampleKey) error {
	return verifySamplesObserving(input, expected, nil)
}

func verifySamplesObserving(input io.Reader, expected []sampleKey, observe func(string) error) error {
	counts := make(map[sampleKey]int, len(expected))
	for _, key := range expected {
		if _, exists := counts[key]; exists {
			return fmt.Errorf("duplicate expected measurement: %+v", key)
		}
		counts[key] = 0
	}
	if err := countSamples(input, counts, observe); err != nil {
		return err
	}
	for key, count := range counts {
		if count != 10 {
			return fmt.Errorf("%s/%s: got %d samples, require exactly 10", key.Package, key.Name, count)
		}
	}
	return nil
}

func countSamples(input io.Reader, counts map[sampleKey]int, observe func(string) error) error {
	scanner := bufio.NewScanner(input)
	packagePath := ""
	var completion benchmarkCompletion
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if isBenchmarkFailure(line) {
			return errors.New("benchmark process failed")
		}
		if err := completion.observe(line); err != nil {
			return err
		}
		if observe != nil {
			if err := observe(line); err != nil {
				return err
			}
		}
		if strings.HasPrefix(line, "pkg: ") {
			packagePath = strings.TrimPrefix(line, "pkg: github.com/light-speak/luxo/")
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		if err := countMeasurement(packagePath, fields, counts); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return completion.finish()
}

func countMeasurement(packagePath string, fields []string, counts map[sampleKey]int) error {
	if err := addMeasurement(make(inventory), fields); err != nil {
		return err
	}
	if err := requireStandardMetrics(fields); err != nil {
		return fmt.Errorf("%s/%s: %w", packagePath, fields[0], err)
	}
	key := sampleKey{Package: packagePath, Name: fields[0]}
	if _, exists := counts[key]; !exists {
		return fmt.Errorf("unexpected benchmark measurement: %+v", key)
	}
	counts[key]++
	return nil
}

// CI uses -benchmem: every sample must retain time, bytes and allocations.
func requireStandardMetrics(fields []string) error {
	var present uint8
	for i := 3; i < len(fields); i += 2 {
		var bit uint8
		switch fields[i] {
		case "ns/op":
			bit = 1
		case "B/op":
			bit = 2
		case "allocs/op":
			bit = 4
		default:
			continue
		}
		if present&bit != 0 {
			return fmt.Errorf("duplicate benchmark metric %q", fields[i])
		}
		present |= bit
	}
	if present != 7 {
		return errors.New("benchmark sample requires ns/op, B/op and allocs/op")
	}
	return nil
}
