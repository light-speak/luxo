package search

import (
	"strings"
	"testing"
)

// ─── PGTsvector ──────────────────────────────────────────────────────────────

func TestPGTsvector_BuildSearchCondition_Default(t *testing.T) {
	pg := &PGTsvector{}
	sql, args := pg.BuildSearchCondition("title", "hello world", 1)
	want := "to_tsvector('simple', title) @@ plainto_tsquery('simple', $1)"
	if sql != want {
		t.Errorf("SQL =\n  %q\nwant\n  %q", sql, want)
	}
	if len(args) != 1 || args[0] != "hello world" {
		t.Errorf("args = %v", args)
	}
}

func TestPGTsvector_BuildSearchCondition_English(t *testing.T) {
	pg := &PGTsvector{Config: "english"}
	sql, args := pg.BuildSearchCondition("content", "search terms", 3)
	if !strings.Contains(sql, "'english'") {
		t.Errorf("expected english config: %s", sql)
	}
	if !strings.Contains(sql, "$3") {
		t.Errorf("expected $3 placeholder: %s", sql)
	}
	if len(args) != 1 || args[0] != "search terms" {
		t.Errorf("args = %v", args)
	}
}

func TestPGTsvector_BuildSearchCondition_HighParamIdx(t *testing.T) {
	pg := &PGTsvector{Config: "simple"}
	sql, _ := pg.BuildSearchCondition("body", "test", 42)
	if !strings.Contains(sql, "$42") {
		t.Errorf("expected $42: %s", sql)
	}
}

func TestPGTsvector_MigrationSQL_Default(t *testing.T) {
	pg := &PGTsvector{}
	stmts := pg.MigrationSQL("posts", "title")
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	stmt := stmts[0]
	if !strings.Contains(stmt, "CREATE INDEX IF NOT EXISTS") {
		t.Errorf("missing IF NOT EXISTS: %s", stmt)
	}
	if !strings.Contains(stmt, "idx_posts_title_search") {
		t.Errorf("missing index name: %s", stmt)
	}
	if !strings.Contains(stmt, "USING gin") {
		t.Errorf("missing GIN: %s", stmt)
	}
	if !strings.Contains(stmt, "to_tsvector('simple', title)") {
		t.Errorf("missing tsvector expression: %s", stmt)
	}
}

func TestPGTsvector_MigrationSQL_English(t *testing.T) {
	pg := &PGTsvector{Config: "english"}
	stmts := pg.MigrationSQL("articles", "body")
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	if !strings.Contains(stmts[0], "'english'") {
		t.Errorf("expected english config: %s", stmts[0])
	}
	if !strings.Contains(stmts[0], "idx_articles_body_search") {
		t.Errorf("wrong index name: %s", stmts[0])
	}
}

func TestPGTsvector_Name(t *testing.T) {
	pg := &PGTsvector{}
	if pg.Name() != "pg_tsvector" {
		t.Errorf("Name() = %q", pg.Name())
	}
}

// ─── Interface compliance ────────────────────────────────────────────────────

func TestPGTsvector_ImplementsSearchBackend(t *testing.T) {
	var _ SearchBackend = (*PGTsvector)(nil)
}
