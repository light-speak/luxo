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
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	var expected expectedSamples
	if err := json.Unmarshal(data, &expected); err != nil {
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

func verifySampleFile(path string, expected []sampleKey) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := verifySamples(file, expected); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func verifySamples(input io.Reader, expected []sampleKey) error {
	counts := make(map[sampleKey]int, len(expected))
	for _, key := range expected {
		if _, exists := counts[key]; exists {
			return fmt.Errorf("duplicate expected measurement: %+v", key)
		}
		counts[key] = 0
	}
	if err := countSamples(input, counts); err != nil {
		return err
	}
	for key, count := range counts {
		if count != 10 {
			return fmt.Errorf("%s/%s: got %d samples, require exactly 10", key.Package, key.Name, count)
		}
	}
	return nil
}

func countSamples(input io.Reader, counts map[sampleKey]int) error {
	scanner := bufio.NewScanner(input)
	packagePath := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "FAIL" || strings.HasPrefix(line, "--- FAIL:") {
			return errors.New("benchmark process failed")
		}
		if strings.HasPrefix(line, "pkg: ") {
			packagePath = strings.TrimPrefix(line, "pkg: github.com/light-speak/luxo/")
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		if err := addMeasurement(make(inventory), fields); err != nil {
			return err
		}
		key := sampleKey{Package: packagePath, Name: fields[0]}
		if _, exists := counts[key]; !exists {
			return fmt.Errorf("unexpected benchmark measurement: %+v", key)
		}
		counts[key]++
	}
	return scanner.Err()
}
