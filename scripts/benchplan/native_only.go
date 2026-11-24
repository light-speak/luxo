package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
)

type nativeOnlySelection struct {
	packagePath string
	platform    map[string]string
	batch       benchShard
	expected    []sampleKey
}

func writeNativeOnlyPlan(args []string, output io.Writer) error {
	if len(args) != 3 {
		return errors.New("usage: benchplan --native-only-plan <inventory.json> <base|head>")
	}
	selection, err := readNativeOnlySelection(args[1], args[2])
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, formatShard(selection.batch))
	return err
}

func verifyNativeOnlyFiles(args []string) error {
	if len(args) != 4 {
		return errors.New("usage: benchplan --verify-native-only <inventory.json> <base|head> <normalized.log>")
	}
	selection, err := readNativeOnlySelection(args[1], args[2])
	if err != nil {
		return err
	}
	if len(selection.expected) == 0 {
		return fmt.Errorf("no native-only measurements expected on %s", args[2])
	}
	metadata := benchmarkMetadata{packagePath: selection.packagePath, platform: make(map[string]string, 3)}
	if err := verifySampleFileObserving(args[3], selection.expected, metadata.observe); err != nil {
		return err
	}
	for _, label := range []string{"goos", "goarch"} {
		if metadata.platform[label] != selection.platform[label] {
			return fmt.Errorf("%s: %s %q differs from inventory %q", args[3], label,
				metadata.platform[label], selection.platform[label])
		}
	}
	// Discovery and sample jobs may run on different CPUs.
	if selection.platform["cpu"] != "" && metadata.platform["cpu"] == "" {
		return fmt.Errorf("%s: missing benchmark CPU", args[3])
	}
	return nil
}

func readNativeOnlySelection(path, side string) (nativeOnlySelection, error) {
	var selection nativeOnlySelection
	if side != "base" && side != "head" {
		return selection, fmt.Errorf("invalid native benchmark side %q", side)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return selection, err
	}
	var receipt frozenInventoryReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return selection, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateNativeOnlyReceipt(receipt); err != nil {
		return selection, fmt.Errorf("%s: %w", path, err)
	}
	native, only := receipt.Native.Base, receipt.NativeBaseOnly
	if side == "head" {
		native, only = receipt.Native.Head, receipt.NativeHeadOnly
	}
	return selectNativeFamilies(receipt, native, only), nil
}

func validateNativeOnlyReceipt(receipt frozenInventoryReceipt) error {
	if !validPackage(receipt.Package) || receipt.Platform["goos"] == "" || receipt.Platform["goarch"] == "" {
		return errors.New("native inventory requires a canonical package and platform")
	}
	if len(receipt.Native.Base) == 0 || len(receipt.Native.Head) == 0 {
		return errors.New("native inventory requires both discovered sides")
	}
	native, err := pairedSampleKeys(receipt.Native)
	if err != nil {
		return err
	}
	for key := range native {
		if key.Package != receipt.Package || !validConfirmationName(key.Name) {
			return fmt.Errorf("invalid native inventory benchmark: %+v", key)
		}
	}
	frozen, err := pairedSampleKeys(receipt.Frozen)
	if err != nil {
		return err
	}
	baseline := make(map[sampleKey]uint8, len(receipt.Native.Base))
	for _, key := range receipt.Native.Base {
		baseline[key] = 0b11
	}
	if !maps.Equal(frozen, baseline) {
		return errors.New("frozen inventory must include every native baseline leaf on both sides")
	}
	return validateNativeDifferences(native, receipt.NativeBaseOnly, receipt.NativeHeadOnly)
}

func validateNativeDifferences(native map[sampleKey]uint8, base, head []sampleKey) error {
	only, err := pairedSampleKeys(expectedSamples{Base: base, Head: head})
	if err != nil {
		return err
	}
	for key, mask := range native {
		if mask == 0b11 {
			continue
		}
		if only[key] != mask {
			return fmt.Errorf("native-only inventory is missing or mislabels %s/%s", key.Package, key.Name)
		}
		delete(only, key)
	}
	if len(only) != 0 {
		return errors.New("native-only inventory includes unexpected leaves")
	}
	return nil
}

func selectNativeFamilies(receipt frozenInventoryReceipt, native, only []sampleKey) nativeOnlySelection {
	selection := nativeOnlySelection{packagePath: receipt.Package, platform: receipt.Platform}
	roots := make(map[string]bool)
	for _, key := range only {
		root := nativeBenchmarkRoot(key.Name)
		if !roots[root] {
			selection.batch.Groups = append(selection.batch.Groups, benchGroup{Package: receipt.Package, Name: root})
			roots[root] = true
		}
	}
	// A sibling may initialize or check shared benchmark fixture state.
	for _, key := range native {
		if roots[nativeBenchmarkRoot(key.Name)] {
			selection.expected = append(selection.expected, key)
		}
	}
	return selection
}

func nativeBenchmarkRoot(name string) string {
	root, _, _ := strings.Cut(strings.TrimSuffix(name, "-2"), "/")
	return root
}
