package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	defaultShardCount = 8
	maxShardCount     = 256
	// Two rounds of ten 200 ms samples for 480 leaves target 32 minutes
	// of measurements, leaving time for compilation and calibration.
	targetMeasurementsPerShard = 480
)

type benchGroup struct {
	Package, Name        string
	Base, Head           int
	Common               bool
	BaseNames, HeadNames []string
}

type benchShard struct {
	ID     string       `json:"shard"`
	Groups []benchGroup `json:"-"`
	Weight int          `json:"-"`
}

func workflowShardLimit(groups []benchGroup) int {
	total := 0
	for _, group := range groups {
		total += group.Base + group.Head
	}
	target := (total + targetMeasurementsPerShard - 1) / targetMeasurementsPerShard
	return min(maxShardCount, max(defaultShardCount, target))
}

func makePlan(groups []benchGroup, limit int) ([]benchShard, error) {
	if limit < 1 || limit > maxShardCount {
		return nil, errors.New("shard limit must be in 1..256")
	}
	groups = slices.Clone(groups)
	slices.SortFunc(groups, compareGroups)
	paired := 0
	for _, group := range groups {
		if group.Common {
			paired++
		}
	}
	if paired == 0 {
		return nil, errors.New("affected benchmarks have no common baseline measurements")
	}
	shards := make([]benchShard, min(limit, paired))
	seeded := 0
	for _, group := range groups {
		index := lightestShard(shards)
		if seeded < len(shards) {
			index = seeded
			seeded++
		}
		shards[index].Groups = append(shards[index].Groups, group)
		shards[index].Weight += group.Base + group.Head
	}
	for i := range shards {
		shards[i].ID = fmt.Sprintf("%02d", i+1)
	}
	return shards, nil
}

func compareGroups(a, b benchGroup) int {
	if a.Common != b.Common {
		if a.Common {
			return -1
		}
		return 1
	}
	if a.Base+a.Head != b.Base+b.Head {
		return b.Base + b.Head - a.Base - a.Head
	}
	if order := strings.Compare(a.Package, b.Package); order != 0 {
		return order
	}
	return strings.Compare(a.Name, b.Name)
}

func lightestShard(shards []benchShard) int {
	index := 0
	for i := range shards {
		if shards[i].Weight < shards[index].Weight {
			index = i
		}
	}
	return index
}
