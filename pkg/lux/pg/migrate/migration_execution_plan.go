package migrate

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type migrationObjectKind int

const (
	migrationNoObject migrationObjectKind = iota
	migrationRelation
	migrationSchema
	migrationDatabase
)

type migrationObjectReference struct {
	kind       migrationObjectKind
	name       string
	start, end int
}

type migrationTablespaceMode int

const (
	migrationNoTablespace migrationTablespaceMode = iota
	migrationDefaultTablespace
	migrationExplicitTablespace
)

type migrationExecutionPlan struct {
	targets         [2]migrationObjectReference
	tablespace      migrationTablespaceMode
	tablespaceName  string
	tempIndependent bool
}

// A valid standalone statement can run even when its syntax cannot be bound for
// later replay. In that case the pending attempt requires operator verification.
func planMigrationExecution(sql string) migrationExecutionPlan {
	c := migrationTokens{sql: sql}
	for c.accept(";") {
	}
	switch strings.ToUpper(c.next()) {
	case "CREATE":
		return c.planCreateIndexExecution()
	case "DROP":
		return c.planDropIndexExecution()
	case "ALTER":
		return c.planDetachExecution()
	case "REINDEX":
		return c.planReindexExecution()
	default:
		return migrationExecutionPlan{}
	}
}

func (c *migrationTokens) planCreateIndexExecution() migrationExecutionPlan {
	c.accept("UNIQUE")
	if !c.accept("INDEX") || !c.accept("CONCURRENTLY") {
		return migrationExecutionPlan{}
	}
	if c.accept("IF") && (!c.accept("NOT") || !c.accept("EXISTS")) {
		return migrationExecutionPlan{}
	}
	if !c.accept("ON") {
		if c.executionObject(migrationSchema).kind == migrationNoObject || !c.accept("ON") {
			return migrationExecutionPlan{}
		}
	}
	c.accept("ONLY")
	table := c.executionObject(migrationRelation)
	if table.kind == migrationNoObject {
		return migrationExecutionPlan{}
	}
	defaultMethod := true
	if c.accept("USING") {
		method := c.next()
		defaultMethod = strings.EqualFold(method, "btree") || method == `"btree"`
	}
	if !c.accept("(") {
		return migrationExecutionPlan{}
	}
	columnsOnly := c.simpleIndexKeys()
	plan := migrationExecutionPlan{targets: [2]migrationObjectReference{table}, tablespace: migrationDefaultTablespace}
	if c.accept("INCLUDE") {
		if !c.accept("(") || !c.finishExecutionGroup() {
			return migrationExecutionPlan{}
		}
	}
	if c.accept("NULLS") {
		c.accept("NOT")
		if !c.accept("DISTINCT") {
			return migrationExecutionPlan{}
		}
	}
	if c.accept("WITH") {
		if !c.accept("(") || !c.finishExecutionGroup() {
			return migrationExecutionPlan{}
		}
	}
	if c.accept("TABLESPACE") {
		space := c.executionObject(migrationSchema)
		if space.kind == migrationNoObject {
			return migrationExecutionPlan{}
		}
		plan.tablespace, plan.tablespaceName = migrationExplicitTablespace, space.name
	}
	plan.tempIndependent = defaultMethod && columnsOnly && !c.accept("WHERE")
	return plan
}

// Stop after the matching parenthesis. An expression, type cast, explicit
// operator class, or collation prevents an independent replay across temp scopes.
func (c *migrationTokens) simpleIndexKeys() bool {
	start := c.pos
	for migrationIdentifier(c.next()) {
		if c.accept(")") {
			return true
		}
		if !c.accept(",") {
			break
		}
	}
	c.pos = start
	c.finishExecutionGroup()
	return false
}

func (c *migrationTokens) finishExecutionGroup() bool {
	depth := 1
	for token := c.next(); token != ""; token = c.next() {
		switch token {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

func (c *migrationTokens) planDropIndexExecution() migrationExecutionPlan {
	if !c.accept("INDEX") || !c.accept("CONCURRENTLY") || c.accept("IF") && !c.accept("EXISTS") {
		return migrationExecutionPlan{}
	}
	index := c.executionObject(migrationRelation)
	// PostgreSQL accepts only one index in a concurrent DROP INDEX.
	if c.accept(",") {
		return migrationExecutionPlan{}
	}
	return migrationExecutionPlan{targets: [2]migrationObjectReference{index}, tempIndependent: true}
}

func (c *migrationTokens) planDetachExecution() migrationExecutionPlan {
	if !c.accept("TABLE") || c.accept("IF") && !c.accept("EXISTS") {
		return migrationExecutionPlan{}
	}
	parenthesized := c.accept("ONLY") && c.accept("(")
	parent := c.executionObject(migrationRelation)
	if parenthesized && !c.accept(")") {
		return migrationExecutionPlan{}
	}
	c.accept("*")
	if parent.kind == migrationNoObject || !c.accept("DETACH") || !c.accept("PARTITION") {
		return migrationExecutionPlan{}
	}
	partition := c.executionObject(migrationRelation)
	if partition.kind == migrationNoObject || !c.accept("CONCURRENTLY") {
		return migrationExecutionPlan{}
	}
	return migrationExecutionPlan{targets: [2]migrationObjectReference{parent, partition}, tempIndependent: true}
}

func (c *migrationTokens) planReindexExecution() migrationExecutionPlan {
	plan := migrationExecutionPlan{tempIndependent: true}
	if c.accept("(") && !c.reindexExecutionOptions(&plan) {
		return migrationExecutionPlan{}
	}
	var kind migrationObjectKind
	switch strings.ToUpper(c.next()) {
	case "INDEX", "TABLE":
		kind = migrationRelation
	case "SCHEMA":
		kind = migrationSchema
	case "DATABASE", "SYSTEM":
		kind = migrationDatabase
	default:
		return migrationExecutionPlan{}
	}
	start := c.pos
	if c.accept("CONCURRENTLY") && c.accept(".") {
		c.pos = start
	}
	peek := *c
	if token := peek.next(); kind == migrationDatabase && (token == "" || token == ";") {
		plan.targets[0] = migrationObjectReference{kind: kind}
		return plan
	}
	plan.targets[0] = c.executionObject(kind)
	return plan
}

func (c *migrationTokens) reindexExecutionOptions(plan *migrationExecutionPlan) bool {
	for {
		option := c.next()
		if option == "" {
			return false
		}
		name, ok := c.reindexOptionString(option)
		if !ok {
			return false
		}
		if strings.EqualFold(name, "tablespace") {
			space := c.executionObject(migrationSchema)
			if space.kind == migrationNoObject {
				return false
			}
			plan.tablespace, plan.tablespaceName = migrationExplicitTablespace, space.name
		}
		for token := c.next(); ; token = c.next() {
			switch token {
			case "":
				return false
			case ")":
				return true
			case ",":
			default:
				continue
			}
			break
		}
	}
}

// Capture both the name used by PostgreSQL's lookup functions and the byte span
// that can be replaced by the qualified name after the identity has been proved.
func (c *migrationTokens) executionObject(kind migrationObjectKind) migrationObjectReference {
	part, start, valid := c.executionIdentifier()
	if !valid {
		return migrationObjectReference{}
	}
	end := c.pos
	if kind == migrationRelation {
		for c.accept(".") {
			next, _, ok := c.executionIdentifier()
			if !ok {
				return migrationObjectReference{}
			}
			part += "." + next
			end = c.pos
		}
	}
	return migrationObjectReference{kind: kind, name: part, start: start, end: end}
}

func (c *migrationTokens) executionIdentifier() (string, int, bool) {
	token := c.next()
	start := c.pos - len(token)
	if c.reindexUnicodePrefix(token, '"') {
		source, escape, ok := c.unicodeEscapeSource()
		if !ok {
			return "", 0, false
		}
		decoded, ok := decodeExecutionIdentifier(strings.ReplaceAll(source, `""`, `"`), escape)
		if !ok {
			return "", 0, false
		}
		token = `"` + strings.ReplaceAll(decoded, `"`, `""`) + `"`
	}
	return token, start, migrationIdentifier(token)
}

func decodeExecutionIdentifier(value string, escape byte) (string, bool) {
	var result strings.Builder
	for {
		i := strings.IndexByte(value, escape)
		if i < 0 {
			result.WriteString(value)
			return result.String(), true
		}
		result.WriteString(value[:i])
		value = value[i+1:]
		if len(value) > 0 && value[0] == escape {
			result.WriteByte(escape)
			value = value[1:]
			continue
		}
		decoded, remaining := executionIdentifierCodepoint(value)
		if utf16.IsSurrogate(decoded) && len(remaining) > 0 && remaining[0] == escape {
			var low rune
			low, remaining = executionIdentifierCodepoint(remaining[1:])
			decoded = utf16.DecodeRune(decoded, low)
			if decoded == utf8.RuneError {
				return "", false
			}
		}
		if decoded == 0 || !utf8.ValidRune(decoded) {
			return "", false
		}
		result.WriteRune(decoded)
		value = remaining
	}
}

func executionIdentifierCodepoint(value string) (rune, string) {
	digits := 4
	if strings.HasPrefix(value, "+") {
		digits = 6
		value = value[1:]
	}
	if len(value) < digits {
		return 0, ""
	}
	number, err := strconv.ParseUint(value[:digits], 16, 32)
	if err != nil {
		return 0, ""
	}
	return rune(number), value[digits:]
}
