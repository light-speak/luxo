package codegen

import "strings"

func (c *compiler) queryAliasModel(name string) string {
	value := c.vars[name]
	if value.isModel || value.isList {
		return ""
	}
	model, ok := strings.CutSuffix(value.name, "QueryBuilder")
	if !ok || c.models[model] == nil {
		return ""
	}
	return model
}

func (c *compiler) queryRootModel(name string) string {
	if c.models[name] != nil {
		return name
	}
	return c.queryAliasModel(name)
}

func (c *compiler) compileQueryAlias(name, model string, links []chainLink) string {
	var b strings.Builder
	b.WriteString(name)
	for i, link := range links {
		if c.compileTerminalMethod(&b, model, link) {
			return b.String()
		}
		c.compileModifierMethod(&b, model, link, i, len(links), true)
	}
	return b.String()
}
