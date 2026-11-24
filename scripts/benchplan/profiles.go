package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

type benchmarkProfile struct{ pkg, profile string }

func profileFor(pkg, name string) benchmarkProfile {
	return benchmarkProfile{pkg, benchlayout.ProfileForBenchmark(pkg, name)}
}

func formatBatches(batches map[benchmarkProfile][]string, prefix, suffix string) string {
	keys := make([]benchmarkProfile, 0, len(batches))
	for key := range batches {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(x, y benchmarkProfile) int {
		return cmp.Or(strings.Compare(x.pkg, y.pkg), strings.Compare(x.profile, y.profile))
	})
	var result strings.Builder
	for _, key := range keys {
		names := batches[key]
		fmt.Fprintf(&result, "%s\t%s\t%s%s%s\n", key.pkg, key.profile, prefix, strings.Join(names, "|"), suffix)
	}
	return result.String()
}

func verifyProfileBatch(args []string) error {
	if len(args) != 4 {
		return errors.New("usage: benchplan --verify-profile-batch <primary|confirmation> <expected.json> <batch.tsv>")
	}
	expected, err := readExpectedSamples(args[2])
	if err != nil {
		return err
	}
	batch, err := os.ReadFile(args[3])
	if err != nil {
		return err
	}
	shard, err := shardForExpected(expected)
	if err != nil {
		return err
	}
	var want string
	switch args[1] {
	case "primary":
		want = formatShard(shard)
	case "confirmation":
		if !slices.Equal(expected.Base, expected.Head) {
			return errors.New("confirmation profiles require the same paired leaves")
		}
		want = formatConfirmationBatch(expected.Base)
	default:
		return fmt.Errorf("unknown benchmark batch phase %q", args[1])
	}
	if string(batch) != want {
		return fmt.Errorf("%s benchmark batch does not match expected leaves and profiles", args[1])
	}
	return nil
}

func shardForExpected(expected expectedSamples) (benchShard, error) {
	var shard benchShard
	roots := make(map[sampleKey]bool)
	for _, side := range [][]sampleKey{expected.Base, expected.Head} {
		if len(side) == 0 {
			return benchShard{}, errors.New("benchmark batch requires base and head leaves")
		}
		for _, leaf := range side {
			if !validPackage(leaf.Package) || !validConfirmationName(leaf.Name) {
				return benchShard{}, fmt.Errorf("invalid benchmark batch leaf: %+v", leaf)
			}
			name, _, _ := strings.Cut(strings.TrimSuffix(leaf.Name, "-2"), "/")
			root := sampleKey{leaf.Package, name}
			if !roots[root] {
				shard.Groups = append(shard.Groups, benchGroup{Package: root.Package, Name: root.Name})
				roots[root] = true
			}
		}
	}
	return shard, nil
}
