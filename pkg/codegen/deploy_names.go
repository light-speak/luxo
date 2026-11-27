package codegen

import (
	"strconv"
	"strings"
)

// Aliases only contain modules whose emitted names would collide in this target.
type deploymentAliases map[string]string

func (aliases deploymentAliases) name(module string) string {
	if aliases != nil {
		if alias := aliases[module]; alias != "" {
			return alias
		}
	}
	return module
}

func planDeploymentAliases(modules []moduleInfo, suffix string, system ...string) deploymentAliases {
	if !deploymentNamesMayCollide(modules, suffix, system) {
		return nil
	}
	claimed := make(map[string]bool, len(system)+len(modules))
	natural := make(map[string]bool, len(system)+len(modules))
	for _, name := range system {
		claimed[name] = true
		natural[name] = true
	}
	for _, module := range modules {
		reserveDeploymentName(natural, strings.ToLower(module.name), suffix)
	}

	var aliases deploymentAliases
	for _, module := range modules {
		name := strings.ToLower(module.name)
		if deploymentNameTaken(claimed, name, suffix) {
			if aliases == nil {
				aliases = make(deploymentAliases)
			}
			name = availableDeploymentAlias(name, suffix, claimed, natural)
			aliases[module.name] = name
		}
		reserveDeploymentName(claimed, name, suffix)
	}
	return aliases
}

func deploymentNamesMayCollide(modules []moduleInfo, suffix string, system []string) bool {
	for _, module := range modules {
		name := strings.ToLower(module.name)
		if name != module.name || suffix != "" && strings.HasSuffix(name, suffix) {
			return true
		}
		for _, reserved := range system {
			if name == reserved || suffix != "" && strings.HasSuffix(reserved, suffix) && name == strings.TrimSuffix(reserved, suffix) {
				return true
			}
		}
	}
	return false
}

func reserveDeploymentName(set map[string]bool, name, suffix string) {
	set[name] = true
	if suffix != "" {
		set[name+suffix] = true
	}
}

func deploymentNameTaken(set map[string]bool, name, suffix string) bool {
	return set[name] || suffix != "" && set[name+suffix]
}

func availableDeploymentAlias(name, suffix string, claimed, natural map[string]bool) string {
	base := "module-" + name
	candidate := base
	for index := 2; deploymentNameTaken(claimed, candidate, suffix) || deploymentNameTaken(natural, candidate, suffix); index++ {
		candidate = base + "-" + strconv.Itoa(index)
	}
	return candidate
}
