package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

type frozenInventoryReceipt struct {
	Package                        string
	Platform                       map[string]string
	Native, Frozen                 expectedSamples
	NativeBaseOnly, NativeHeadOnly []sampleKey
}

func verifyFrozenInventoryFiles(args []string, output io.Writer) error {
	if len(args) != 6 || !validPackage(args[1]) {
		return errors.New("usage: benchplan --verify-frozen-inventory <package> <native-base.log> <native-head.log> <frozen-base.log> <frozen-head.log>")
	}
	packagePath := args[1]
	var inventories [4]map[string]struct{}
	var platform map[string]string
	roles := [...]string{"native base", "native head", "frozen base", "frozen head"}
	for i, path := range args[2:] {
		leaves, fields, err := readFrozenRole(packagePath, path)
		if err != nil {
			return fmt.Errorf("%s inventory %s: %w", roles[i], path, err)
		}
		if i == 0 {
			platform = fields
		} else if !maps.Equal(platform, fields) {
			return fmt.Errorf("benchmark platforms differ for %s: %v != %v", roles[i], fields, platform)
		}
		inventories[i] = leaves
	}
	nativeBase, nativeHead := inventories[0], inventories[1]
	for i := 2; i < len(inventories); i++ {
		if frozen := inventories[i]; !maps.Equal(nativeBase, frozen) {
			return fmt.Errorf("%s inventory differs from native base: missing %v, extra %v",
				roles[i], unmatchedLeaves(nativeBase, frozen), unmatchedLeaves(frozen, nativeBase))
		}
	}
	base := frozenSampleKeys(packagePath, slices.Sorted(maps.Keys(nativeBase)))
	head := frozenSampleKeys(packagePath, slices.Sorted(maps.Keys(nativeHead)))
	return json.NewEncoder(output).Encode(frozenInventoryReceipt{
		Package: packagePath, Platform: platform,
		Native:         expectedSamples{Base: base, Head: head},
		Frozen:         expectedSamples{Base: base, Head: base},
		NativeBaseOnly: frozenSampleKeys(packagePath, unmatchedLeaves(nativeBase, nativeHead)),
		NativeHeadOnly: frozenSampleKeys(packagePath, unmatchedLeaves(nativeHead, nativeBase)),
	})
}

func unmatchedLeaves(left, right map[string]struct{}) []string {
	var names []string
	for name := range left {
		if _, exists := right[name]; !exists {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func frozenSampleKeys(packagePath string, leaves []string) []sampleKey {
	keys := make([]sampleKey, len(leaves))
	for i, leaf := range leaves {
		keys[i] = sampleKey{Package: packagePath, Name: leaf}
	}
	return keys
}

type benchmarkMetadata struct {
	packagePath         string
	inPackage           bool
	platform            map[string]string
	requireOneIteration bool
}

func readFrozenRole(packagePath, path string) (map[string]struct{}, map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	metadata := benchmarkMetadata{
		packagePath: packagePath, platform: make(map[string]string, 3), requireOneIteration: true,
	}
	groups, err := readInventoryObserving(file, metadata.observe)
	if err != nil {
		return nil, nil, err
	}
	if metadata.platform["goos"] == "" || metadata.platform["goarch"] == "" {
		return nil, nil, errors.New("missing benchmark platform")
	}
	leaves := make(map[string]struct{})
	for _, group := range groups {
		maps.Copy(leaves, group)
	}
	return leaves, metadata.platform, nil
}

func (metadata *benchmarkMetadata) observe(line string) error {
	label, value, _ := strings.Cut(line, ": ")
	switch label {
	case "goos", "goarch", "cpu":
		prior, known := metadata.platform[label]
		if value == "" || (known && value != prior) {
			return fmt.Errorf("missing or mixed benchmark platform field %s", label)
		}
		metadata.platform[label] = value
	case "pkg":
		if value != "github.com/light-speak/luxo/"+metadata.packagePath {
			return fmt.Errorf("unexpected benchmark package %q, want %q", value, metadata.packagePath)
		}
		metadata.inPackage = true
	case "PASS":
		metadata.inPackage = false
	default:
		if strings.HasPrefix(line, "Benchmark") {
			return metadata.measurement(strings.Fields(line))
		}
	}
	return nil
}

func (metadata *benchmarkMetadata) measurement(fields []string) error {
	if !metadata.inPackage {
		return errors.New("measurement has no benchmark package identity")
	}
	if !metadata.requireOneIteration {
		return nil
	}
	if len(fields) < 2 || fields[1] != "1" {
		return errors.New("benchmark inventory requires one iteration per leaf")
	}
	return requireStandardMetrics(fields)
}
