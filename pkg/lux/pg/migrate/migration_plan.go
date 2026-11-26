package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/internal/sqllex"
)

type rollbackFile struct {
	path     string
	checksum *string
}

type plannedMigration struct {
	name     string
	sql      string
	checksum string
	mode     migrationMode
}

type migrationMode struct {
	autocommit bool
	index      string
	table      string
}

var errSplitMigration = errors.New("split this section into numbered migration files, with each standalone statement in its own file")

func planPendingFiles(paths []string, withChecksum bool) ([]plannedMigration, error) {
	plan := make([]plannedMigration, 0, len(paths))
	for _, path := range paths {
		migration, err := planMigration(path, "UP", withChecksum, nil)
		if err != nil {
			return nil, migrationPlanError(path, "UP", err)
		}
		plan = append(plan, migration)
	}
	return plan, nil
}

func planRollbackFiles(files []rollbackFile) ([]plannedMigration, error) {
	plan := make([]plannedMigration, 0, len(files))
	for _, file := range files {
		migration, err := planMigration(file.path, "DOWN", false, file.checksum)
		if err != nil {
			return nil, migrationPlanError(file.path, "DOWN", err)
		}
		plan = append(plan, migration)
	}
	return plan, nil
}

func migrationPlanError(path, direction string, err error) error {
	if direction == "DOWN" && errors.Is(err, errSplitMigration) {
		err = fmt.Errorf("%w; for an already applied migration, create new compensating migrations without editing applied files", err)
	}
	return fmt.Errorf("%s: %s: %w", filepath.Base(path), direction, err)
}

func planMigration(path, direction string, withChecksum bool, recordedChecksum *string) (plannedMigration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return plannedMigration{}, err
	}
	var checksum string
	if withChecksum || recordedChecksum != nil {
		checksum = migrationChecksum(data)
		if recordedChecksum != nil && *recordedChecksum != checksum {
			return plannedMigration{}, fmt.Errorf("migration file has been modified (checksum mismatch: stored=%s, current=%s)", *recordedChecksum, checksum)
		}
	}
	sql, err := migrationSection(string(data), direction)
	if err != nil {
		return plannedMigration{}, err
	}
	mode, err := planMigrationSQL(sql)
	if err != nil {
		return plannedMigration{}, err
	}
	return plannedMigration{name: filepath.Base(path), sql: sql, checksum: checksum, mode: mode}, nil
}

func migrationSection(content, direction string) (string, error) {
	marker := "-- ====== " + direction + " ======"
	body := -1
	for i := 0; i < len(content); {
		end := skipMigrationToken(content, i)
		if end == i {
			i++
			continue
		}
		token := content[i:end]
		if strings.HasPrefix(token, "-- ======") && migrationHeaderStartsLine(content, i) {
			if body >= 0 {
				return strings.TrimSpace(content[body:i]), nil
			}
			if strings.TrimSpace(token) == marker {
				body = end
			}
		}
		i = end
	}
	if body >= 0 {
		return strings.TrimSpace(content[body:]), nil
	}
	return "", fmt.Errorf("section %s not found", direction)
}

func migrationHeaderStartsLine(content string, index int) bool {
	line := strings.LastIndexAny(content[:index], "\r\n") + 1
	return strings.TrimSpace(content[line:index]) == ""
}

type migrationSQLKind uint8

const (
	migrationSQLComment migrationSQLKind = iota
	migrationSQLTransaction
	migrationSQLAutocommit
	migrationSQLGuardedCreate
)

func planMigrationSQL(sql string) (migrationMode, error) {
	var mode migrationMode
	var statements, firstAutocommit int
	var guarded bool
	for start := 0; start < len(sql); {
		end := migrationStatementEnd(sql, start)
		cursor := migrationTokens{sql: sql[start:end]}
		start = end + 1
		kind := cursor.firstStatementKind()
		if kind == migrationSQLComment {
			continue
		}
		statements++
		if kind == migrationSQLTransaction || firstAutocommit != 0 {
			continue
		}
		firstAutocommit = statements
		mode.autocommit = true
		guarded = kind == migrationSQLGuardedCreate
		if guarded {
			mode.index, mode.table = cursor.indexTarget()
		}
	}
	if firstAutocommit != 0 && statements > 1 {
		return migrationMode{}, fmt.Errorf("statement %d must run outside a transaction; %w", firstAutocommit, errSplitMigration)
	}
	if guarded && mode.table == "" {
		return migrationMode{}, fmt.Errorf("statement %d: cannot inspect CREATE INDEX CONCURRENTLY IF NOT EXISTS target; use standard PostgreSQL identifiers or remove IF NOT EXISTS", firstAutocommit)
	}
	return mode, nil
}

func migrationStatementEnd(sql string, start int) int {
	depth := 0
	for i := start; i < len(sql); {
		if next := skipMigrationToken(sql, i); next != i {
			i = next
			continue
		}
		switch sql[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ';':
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return len(sql)
}

func skipMigrationToken(sql string, index int) int {
	switch sql[index] {
	case '-', '/':
		end, _ := sqllex.SkipComment(sql, index)
		return end
	case '\'', '$':
		end, _ := sqllex.SkipLiteral(sql, index)
		return end
	case '"':
		return sqllex.SkipQuoted(sql, index+1, '"', false)
	default:
		return index
	}
}

type migrationTokens struct {
	sql string
	pos int
}

func (c *migrationTokens) next() string {
	for c.pos < len(c.sql) {
		if end, ok := sqllex.SkipComment(c.sql, c.pos); ok {
			c.pos = end
			continue
		}
		if strings.ContainsRune(" \t\r\n\f\v", rune(c.sql[c.pos])) {
			c.pos++
			continue
		}
		break
	}
	if c.pos == len(c.sql) {
		return ""
	}
	start := c.pos
	if end, ok := sqllex.SkipLiteral(c.sql, start); ok {
		c.pos = end
	} else if c.sql[start] == '"' {
		c.pos = sqllex.SkipQuoted(c.sql, start+1, '"', false)
	} else {
		c.pos++
		if sqllex.IsIdentifierStart(c.sql[start]) {
			for c.pos < len(c.sql) && sqllex.IsIdentifierByte(c.sql[c.pos]) {
				c.pos++
			}
		}
	}
	return c.sql[start:c.pos]
}

func (c *migrationTokens) accept(keyword string) bool {
	start := c.pos
	if strings.EqualFold(c.next(), keyword) {
		return true
	}
	c.pos = start
	return false
}

func (c *migrationTokens) firstStatementKind() migrationSQLKind {
	token := c.next()
	switch {
	case token == "":
		return migrationSQLComment
	case strings.EqualFold(token, "CREATE"):
		c.accept("UNIQUE")
		if c.accept("INDEX") && c.accept("CONCURRENTLY") {
			if c.accept("IF") && c.accept("NOT") && c.accept("EXISTS") {
				return migrationSQLGuardedCreate
			}
			return migrationSQLAutocommit
		}
	case strings.EqualFold(token, "DROP"):
		if c.accept("INDEX") && c.accept("CONCURRENTLY") {
			return migrationSQLAutocommit
		}
	case strings.EqualFold(token, "REINDEX"):
		if c.reindexAutocommit() {
			return migrationSQLAutocommit
		}
	case strings.EqualFold(token, "ALTER"):
		if c.concurrentDetach() {
			return migrationSQLAutocommit
		}
	}
	return migrationSQLTransaction
}

func (c *migrationTokens) concurrentDetach() bool {
	if !c.accept("TABLE") {
		return false
	}
	if c.accept("IF") && !c.accept("EXISTS") {
		return false
	}
	if c.accept("ONLY") && c.accept("(") {
		c.skipRelation()
		if !c.accept(")") {
			return false
		}
	} else {
		c.skipRelation()
		c.accept("*")
	}
	if !c.accept("DETACH") || !c.accept("PARTITION") {
		return false
	}
	c.skipRelation()
	return c.accept("CONCURRENTLY")
}

func (c *migrationTokens) indexTarget() (string, string) {
	index := c.next()
	if !migrationIdentifier(index) || !c.accept("ON") {
		return "", ""
	}
	c.accept("ONLY")
	var table strings.Builder
	for {
		part := c.next()
		if !migrationIdentifier(part) {
			return index, ""
		}
		table.WriteString(part)
		if !c.accept(".") {
			break
		}
		table.WriteByte('.')
	}
	if c.accept("USING") && !migrationIdentifier(c.next()) {
		return index, ""
	}
	if !c.accept("(") {
		return index, ""
	}
	return index, table.String()
}

func migrationIdentifier(token string) bool {
	return token != "" && (token[0] == '"' || sqllex.IsIdentifierStart(token[0]))
}

func (c *migrationTokens) skipRelation() {
	c.next()
	for c.accept(".") {
		c.next()
	}
}
