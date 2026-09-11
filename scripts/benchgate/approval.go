package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type costKey struct {
	Package   string
	Benchmark string
}

type approvedCost struct {
	ID        string `json:"id"`
	BaseSHA   string `json:"baseSHA"`
	Package   string `json:"package"`
	Benchmark string `json:"benchmark"`
	MaxBytes  int64  `json:"maxBytes"`
	MaxAllocs int64  `json:"maxAllocs"`
	Reason    string `json:"reason"`
}

type costPolicy struct {
	Version   int            `json:"version"`
	Approvals []approvedCost `json:"approvals"`
}

type gateOptions struct {
	confirmation bool
	costs        map[costKey]approvedCost
}

func parseGateOptions(args []string) (gateOptions, []string, error) {
	var options gateOptions
	var path, base string
	flags := flag.NewFlagSet("benchgate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&options.confirmation, strings.TrimPrefix(confirmationFlag, "--"), false, "require a second sample group")
	flags.StringVar(&path, "approved-costs", "", "reviewed compilation cost policy")
	flags.StringVar(&base, "base-sha", "", "exact benchmark baseline commit")
	if err := flags.Parse(args); err != nil {
		return options, nil, fmt.Errorf("%w: %v", usageError(), err)
	}
	if path == "" && base == "" {
		return options, flags.Args(), nil
	}
	if path == "" || !validCommitSHA(base) {
		return options, nil, errors.New("approved costs require a policy path and an exact 40-character baseline SHA")
	}
	costs, err := loadApprovedCosts(path, base)
	options.costs = costs
	return options, flags.Args(), err
}

func validCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func loadApprovedCosts(path, base string) (map[costKey]approvedCost, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var policy costPolicy
	if err := decoder.Decode(&policy); err != nil {
		return nil, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("approved cost policy contains trailing content")
	}
	return policy.forBaseline(base)
}

func (policy costPolicy) forBaseline(base string) (map[costKey]approvedCost, error) {
	if policy.Version != 1 {
		return nil, fmt.Errorf("unsupported approved cost policy version %d", policy.Version)
	}
	costs := make(map[costKey]approvedCost)
	ids := make(map[string]bool)
	keys := make(map[string]bool)
	for _, cost := range policy.Approvals {
		if err := cost.validate(); err != nil {
			return nil, err
		}
		identity := cost.BaseSHA + "/" + cost.Package + "/" + cost.Benchmark
		if ids[cost.ID] || keys[identity] {
			return nil, fmt.Errorf("duplicate approved cost %q", cost.ID)
		}
		ids[cost.ID], keys[identity] = true, true
		if cost.BaseSHA == base {
			costs[costKey{cost.Package, cost.Benchmark}] = cost
		}
	}
	return costs, nil
}

func (cost approvedCost) validate() error {
	if strings.TrimSpace(cost.ID) == "" || strings.TrimSpace(cost.Reason) == "" || !validCommitSHA(cost.BaseSHA) {
		return errors.New("approved cost requires an ID, reason and exact baseline SHA")
	}
	switch cost.Package {
	case "github.com/light-speak/luxo/pkg/semantic", "github.com/light-speak/luxo/pkg/codegen":
	default:
		return fmt.Errorf("approved costs cannot exempt package %q; only compiler work is eligible", cost.Package)
	}
	if cost.Benchmark == "" || strings.ContainsAny(cost.Benchmark, "*?[] \t\r\n") {
		return errors.New("approved cost requires an exact benchmark name, without wildcards")
	}
	if cost.MaxBytes < 0 || cost.MaxAllocs < 0 || cost.MaxBytes == 0 && cost.MaxAllocs == 0 {
		return errors.New("approved cost requires nonnegative allocation limits and at least one positive limit")
	}
	return nil
}

// An approval applies only after both complete sample groups have been compared.
func applyApprovedCosts(result *evaluation, primary, confirmation evaluation, costs map[costKey]approvedCost, output io.Writer) error {
	remaining := result.Regressions[:0]
	for _, item := range result.Regressions {
		cost, exists := costs[costKey{item.Key.Package, item.Key.Benchmark}]
		if !exists || item.Key.Unit == "sec/op" {
			remaining = append(remaining, item)
			continue
		}
		accepted, err := cost.accepts(primary, confirmation)
		if err != nil {
			return err
		}
		if !accepted {
			remaining = append(remaining, item)
			continue
		}
		if _, err := fmt.Fprintf(output, "approved compilation cost: %s; %s; limits +%d B/op, +%d allocs/op; baseline %s; %s\n", cost.ID, item.Description, cost.MaxBytes, cost.MaxAllocs, cost.BaseSHA, cost.Reason); err != nil {
			return err
		}
	}
	result.Regressions = remaining
	return nil
}

func (cost approvedCost) accepts(groups ...evaluation) (bool, error) {
	for _, group := range groups {
		for _, limit := range []struct {
			unit    string
			maximum int64
		}{{"B/op", cost.MaxBytes}, {"allocs/op", cost.MaxAllocs}} {
			key := comparisonKey{cost.Package, cost.Benchmark, limit.unit}
			values, exists := group.Measurements[key]
			if !exists {
				return false, fmt.Errorf("approved cost %q requires both B/op and allocs/op measurements", cost.ID)
			}
			if values.Head-values.Base > float64(limit.maximum) {
				return false, nil
			}
		}
	}
	return true, nil
}
