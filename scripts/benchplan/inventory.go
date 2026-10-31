package main

import (
	"bufio"
	"errors"
	"fmt"
	"go/token"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

type inventory map[string]map[string]struct{}

func readInventory(input io.Reader) (inventory, error) {
	result := make(inventory)
	scanner := bufio.NewScanner(input)
	passed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "FAIL" || strings.HasPrefix(line, "--- FAIL:") {
			return nil, errors.New("benchmark inventory failed")
		}
		if line == "PASS" {
			passed = true
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		if err := addMeasurement(result, strings.Fields(line)); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !passed || len(result) == 0 {
		return nil, errors.New("inventory requires successful benchmark measurements and PASS")
	}
	return result, nil
}

func addMeasurement(result inventory, fields []string) error {
	if len(fields) < 4 || !strings.HasSuffix(fields[0], "-2") || !measurementTail(fields[1:]) {
		return fmt.Errorf("invalid benchmark measurement: %q", fields)
	}
	name, _, _ := strings.Cut(strings.TrimSuffix(fields[0], "-2"), "/")
	if !token.IsIdentifier(name) || name == "Benchmark" {
		return fmt.Errorf("invalid benchmark name %q", name)
	}
	if result[name] == nil {
		result[name] = make(map[string]struct{})
	}
	if _, exists := result[name][fields[0]]; exists {
		return fmt.Errorf("duplicate benchmark measurement %q", fields[0])
	}
	result[name][fields[0]] = struct{}{}
	return nil
}

func measurementTail(fields []string) bool {
	if len(fields) < 3 || len(fields)%2 != 1 {
		return false
	}
	iterations, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || iterations == 0 {
		return false
	}
	if fields[2] != "ns/op" && fields[2] != "B/op" {
		return false
	}
	for i := 1; i < len(fields); i += 2 {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func joinInventories(packagePath string, base, head inventory) []benchGroup {
	groups := make(map[string]benchGroup)
	for name, leaves := range base {
		groups[name] = benchGroup{Package: packagePath, Name: name, Base: len(leaves), BaseNames: leafNames(leaves)}
	}
	for name, leaves := range head {
		group := groups[name]
		group.Package, group.Name, group.Head = packagePath, name, len(leaves)
		group.HeadNames = leafNames(leaves)
		for leaf := range leaves {
			if _, exists := base[name][leaf]; exists {
				group.Common = true
				break
			}
		}
		groups[name] = group
	}
	result := make([]benchGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}
	return result
}

func leafNames(leaves map[string]struct{}) []string {
	result := make([]string, 0, len(leaves))
	for name := range leaves {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}
