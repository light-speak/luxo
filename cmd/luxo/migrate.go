package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/env"
	"github.com/light-speak/luxo/pkg/lux/pg"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Database migration commands / 数据库迁移命令",
	Long: `Manage database migrations.
管理数据库迁移。

Commands / 子命令:
  diff [name]    Generate migration from origin changes / 从 origin 变更生成迁移
  up             Apply pending migrations / 执行未执行的迁移
  status         Show migration status / 查看迁移状态`,
}

var migrateDiffCmd = &cobra.Command{
	Use:   "diff [name]",
	Short: "Generate migration SQL from origin diff / 从 origin 差异生成迁移 SQL",
	Long: `Compare current .luxo origin with the last migration state and generate SQL.
对比当前 .luxo origin 和上次迁移状态，生成差异 SQL。

Dangerous operations (DROP TABLE, DROP COLUMN) are commented out by default.
破坏性操作（DROP TABLE、DROP COLUMN）默认注释掉。

Example / 示例:
  luxo migrate diff
  luxo migrate diff add_user_avatar`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMigrateDiff,
}

var dryRun bool

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Apply pending migrations / 执行未执行的迁移",
	Args:  cobra.NoArgs,
	RunE:  runMigrateUp,
}

var migrateDownCmd = &cobra.Command{
	Use:   "down [N]",
	Short: "Rollback last N migrations (default 1) / 回滚最近 N 次迁移（默认 1）",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runMigrateDown,
}

var migrateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show migration status / 查看迁移状态",
	Args:  cobra.NoArgs,
	RunE:  runMigrateStatus,
}

var migrateVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify DB matches expected origin / 验证数据库与预期 origin 一致",
	Args:  cobra.NoArgs,
	RunE:  runMigrateVerify,
}

var migrateSquashCmd = &cobra.Command{
	Use:   "squash",
	Short: "Squash all migrations into one / 合并所有迁移为一个",
	Args:  cobra.NoArgs,
	RunE:  runMigrateSquash,
}

func init() {
	migrateUpCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show SQL without executing / 显示 SQL 但不执行")
	migrateCmd.AddCommand(migrateDiffCmd)
	migrateCmd.AddCommand(migrateUpCmd)
	migrateCmd.AddCommand(migrateDownCmd)
	migrateCmd.AddCommand(migrateStatusCmd)
	migrateCmd.AddCommand(migrateVerifyCmd)
	migrateCmd.AddCommand(migrateSquashCmd)
	rootCmd.AddCommand(migrateCmd)
}

func runMigrateDiff(cmd *cobra.Command, args []string) error {
	green := "\033[32m"
	bold := "\033[1m"
	reset := "\033[0m"

	result, err := parseAndAnalyze()
	if err != nil {
		return err
	}

	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	dialect, err := loadDialect()
	if err != nil {
		return err
	}
	enums := codegen.CollectEnumsFromResult(result)
	desired, err := codegen.ComputeState(result, enums, dialect)
	if err != nil {
		return fmt.Errorf("compute migration state: %w", err)
	}

	statePath := filepath.Join("migrations", ".state.json")
	current, err := codegen.LoadState(statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Build field ID maps from luxo.lock for rename detection
	fieldIDs := buildFieldIDMaps()
	ops, err := codegen.Diff(current, desired, fieldIDs)
	if err != nil {
		return fmt.Errorf("diff migration state: %w", err)
	}
	if len(ops) == 0 {
		fmt.Printf("\n  %s✓ No changes detected / 无变更%s\n\n", green, reset)
		return nil
	}

	name := codegen.AutoMigrationName(ops)
	if len(args) > 0 {
		name = args[0]
	}
	if err := validateMigrationName(name); err != nil {
		return err
	}

	ctx := context.Background()
	if cmd != nil {
		ctx = cmd.Context()
	}
	var migrationPath string
	err = withMigrationGenerationLock(ctx, func() error {
		current, err := codegen.LoadState(statePath)
		if err != nil {
			return fmt.Errorf("load state: %w", err)
		}
		ops, err = codegen.Diff(current, desired, buildFieldIDMaps())
		if err != nil {
			return fmt.Errorf("diff migration state: %w", err)
		}
		if len(ops) == 0 {
			return nil
		}
		if len(args) == 0 {
			name = codegen.AutoMigrationName(ops)
		}
		migrationPath, err = writeMigrationWithState(desired, func() (string, error) {
			return writeMigrationFile(ops, desired, name, dialect)
		})
		return err
	})
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		fmt.Printf("\n  %s✓ No changes detected / 无变更%s\n\n", green, reset)
		return nil
	}

	fmt.Printf("\n  %s%s✓ Migration generated / 迁移已生成%s\n\n", bold, green, reset)
	fmt.Printf("  %s+%s %s\n", green, reset, migrationPath)
	printDiffOps(ops)
	fmt.Println()
	return nil
}

func parseAndAnalyze() (*semantic.Result, error) {
	originFiles, err := findSchemaFiles()
	if err != nil {
		return nil, err
	}
	files, err := parseAllFiles(originFiles)
	if err != nil {
		return nil, err
	}
	return analyzeFiles(files)
}

type migrationOutput struct {
	fileName string
	header   string
}

func writeMigrationFile(ops []codegen.DiffOp, desired *codegen.SchemaState, name string, dialect lux.Dialect) (string, error) {
	if err := validateMigrationName(name); err != nil {
		return "", err
	}
	sequence, err := nextMigrationSeq()
	if err != nil {
		return "", err
	}
	output := migrationOutput{
		fileName: fmt.Sprintf("%s_%03d_%s.sql", time.Now().Format("20060102"), sequence, name),
		header:   "-- Migration: " + name + "\n-- Generated by luxo migrate diff\n\n",
	}
	return writeMigrationOutput(ops, desired, dialect, output)
}

func validateMigrationName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\\\x00\r\n") || !utf8.ValidString(name) {
		return fmt.Errorf("migration name must be one non-empty UTF-8 filename component without line breaks: %q / 迁移名称必须是非空UTF-8文件名片段且不能换行", name)
	}
	return nil
}

func writeMigrationOutput(ops []codegen.DiffOp, desired *codegen.SchemaState, dialect lux.Dialect, output migrationOutput) (string, error) {
	if !filepath.IsLocal(output.fileName) || filepath.Base(output.fileName) != output.fileName {
		return "", fmt.Errorf("migration output must be one filename: %q", output.fileName)
	}
	up, down, err := codegen.GenerateMigrationSQL(ops, desired, dialect)
	if err != nil {
		return "", fmt.Errorf("generate migration SQL: %w", err)
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return "", err
	}
	defer root.Close()
	path := filepath.Join("migrations", output.fileName)
	var content strings.Builder
	content.WriteString(output.header)
	content.WriteString("-- ====== UP ======\n\n")
	content.WriteString(up)
	content.WriteString("\n-- ====== DOWN ======\n\n")
	content.WriteString(down)
	if err := writeNewModuleFile(root, path, []byte(content.String())); err != nil {
		return "", err
	}
	return path, nil
}

func writeInitialMigration(desired *codegen.SchemaState, dialect lux.Dialect, output migrationOutput) (string, error) {
	empty := &codegen.SchemaState{Models: make(map[string]*codegen.ModelState)}
	ops, err := codegen.Diff(empty, desired, nil)
	if err != nil {
		return "", fmt.Errorf("diff migration state: %w", err)
	}
	return writeMigrationOutput(ops, desired, dialect, output)
}

func writeMigrationWithState(desired *codegen.SchemaState, writeSQL func() (string, error)) (path string, err error) {
	data, err := codegen.MarshalState(desired)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return "", err
	}
	defer root.Close()
	created := false
	if err := root.Mkdir("migrations", 0755); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return "", err
	}
	defer func() {
		if err != nil && created {
			err = errors.Join(err, root.Remove("migrations"))
		}
	}()
	state, err := stageMigrationState(root, data)
	if err != nil {
		return "", err
	}
	defer func() {
		if state.temporary != "" {
			err = errors.Join(err, state.removeTemporary())
		}
	}()
	path, err = writeSQL()
	if err != nil {
		return "", err
	}
	// Update state
	if err := state.commit(); err != nil {
		return "", errors.Join(err, removeModuleFiles(root, []string{path}))
	}
	state.temporary = ""
	return path, nil
}

type migrationStateStage struct {
	root      *os.Root
	temporary string
	target    string
	linked    bool
}

func (state migrationStateStage) commit() error {
	if state.linked {
		return os.Rename(state.temporary, state.target)
	}
	return state.root.Rename(state.temporary, state.target)
}

func (state migrationStateStage) removeTemporary() error {
	if state.linked {
		return os.Remove(state.temporary)
	}
	return removeModuleFiles(state.root, []string{state.temporary})
}

func stageMigrationState(root *os.Root, data []byte) (migrationStateStage, error) {
	state, info, err := migrationStateTarget(root)
	if err != nil {
		return state, err
	}
	mode, err := state.mode(info)
	if err != nil {
		return state, err
	}
	state.temporary = filepath.Join(filepath.Dir(state.target), ".state-"+rand.Text()+".tmp")
	if state.linked {
		return state, state.writeLinked(data, mode)
	}
	if err := writeNewProjectFile(state.root, state.temporary, data, mode); err != nil {
		return state, err
	}
	return state, nil
}

func (state migrationStateStage) writeLinked(data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(state.temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := writeProjectFileContent(file, data); err != nil {
		return errors.Join(err, state.removeTemporary())
	}
	return nil
}

func migrationStateTarget(root *os.Root) (state migrationStateStage, info os.FileInfo, err error) {
	state = migrationStateStage{root: root, target: "migrations/.state.json"}
	info, err = root.Lstat(state.target)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil, nil
	}
	if err != nil {
		return state, nil, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return state, info, nil
	}
	// Preserve the configured link and stage beside its actual cache target.
	target, info, err := resolveMigrationStatePath(filepath.Join(root.Name(), state.target))
	if err != nil {
		return state, nil, err
	}
	state.linked, state.target = true, target
	return state, info, nil
}

func resolveMigrationStatePath(path string) (string, os.FileInfo, error) {
	seen := make(map[string]bool)
	for {
		dir, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", nil, err
		}
		path = filepath.Join(dir, filepath.Base(path))
		if seen[path] {
			return "", nil, fmt.Errorf("migration state symlink cycle: %s", path)
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			absolute, err := filepath.Abs(path)
			return absolute, nil, err
		}
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			absolute, err := filepath.Abs(path)
			return absolute, info, err
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", nil, err
		}
		path = link
		if !filepath.IsAbs(link) {
			path = filepath.Join(dir, link)
		}
	}
}

func (state migrationStateStage) mode(info os.FileInfo) (os.FileMode, error) {
	if info == nil {
		return 0644, nil
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("migration state must be a regular file")
	}
	var file *os.File
	var err error
	if state.linked {
		file, err = os.OpenFile(state.target, os.O_WRONLY, 0)
	} else {
		file, err = state.root.OpenFile(state.target, os.O_WRONLY, 0)
	}
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), file.Close()
}

func printDiffOps(ops []codegen.DiffOp) {
	green := "\033[32m"
	cyan := "\033[36m"
	yellow := "\033[33m"
	red := "\033[31m"
	dim := "\033[2m"
	reset := "\033[0m"

	for _, op := range ops {
		switch op.Kind {
		case codegen.CreateTable:
			fmt.Printf("    %sCREATE TABLE%s  %s\n", green, reset, op.Table)
		case codegen.DropTable:
			fmt.Printf("    %sDROP TABLE%s    %s %s(commented out)%s\n", red, reset, op.Table, dim, reset)
		case codegen.AddColumn:
			fmt.Printf("    %sADD COLUMN%s    %s.%s\n", green, reset, op.Table, op.Column)
		case codegen.DropColumn:
			fmt.Printf("    %sDROP COLUMN%s   %s.%s %s(commented out)%s\n", red, reset, op.Table, op.Column, dim, reset)
		case codegen.RenameColumn:
			fmt.Printf("    %sRENAME COLUMN%s %s.%s → %s\n", cyan, reset, op.Table, op.OldName, op.Column)
		case codegen.AlterColumn:
			fmt.Printf("    %sALTER COLUMN%s  %s.%s\n", yellow, reset, op.Table, op.Column)
		case codegen.AddIndex:
			fmt.Printf("    %sADD INDEX%s     %s\n", green, reset, op.Index.Name)
		case codegen.DropIndex:
			fmt.Printf("    %sDROP INDEX%s    %s\n", yellow, reset, op.Index.Name)
		}
		if op.Warning != "" {
			fmt.Printf("      %s⚠ %s%s\n", yellow, op.Warning, reset)
		}
	}
}

func runMigrateUp(cmd *cobra.Command, args []string) error {
	green := "\033[32m"
	cyan := "\033[36m"
	bold := "\033[1m"
	dim := "\033[2m"
	reset := "\033[0m"

	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	ctx := cmd.Context()

	runner, err := migrate.New(ctx, "migrations")
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer runner.Close()

	if dryRun {
		names, sqls, err := runner.DryRun(ctx)
		if err != nil {
			return fmt.Errorf("dry run: %w", err)
		}
		if len(names) == 0 {
			fmt.Printf("\n  %s✓ No pending migrations / 无待执行的迁移%s\n\n", green, reset)
			return nil
		}
		fmt.Printf("\n  %s%s== DRY RUN ==%s\n\n", bold, cyan, reset)
		for i, name := range names {
			fmt.Printf("  %s--%s %s%s%s\n", cyan, reset, dim, name, reset)
			fmt.Printf("  %s\n\n", sqls[i])
		}
		return nil
	}

	executed, err := runner.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}

	if len(executed) == 0 {
		fmt.Printf("\n  %s✓ No pending migrations / 无待执行的迁移%s\n\n", green, reset)
		return nil
	}

	fmt.Printf("\n  %s%s✓ %d migration(s) applied / 已执行 %d 个迁移%s\n\n", bold, green, len(executed), len(executed), reset)
	for _, name := range executed {
		fmt.Printf("  %s✓%s %s%s%s\n", green, reset, dim, name, reset)
	}
	fmt.Println()
	return nil
}

func runMigrateDown(cmd *cobra.Command, args []string) error {
	yellow := "\033[33m"
	bold := "\033[1m"
	dim := "\033[2m"
	reset := "\033[0m"

	n := 1
	if len(args) > 0 {
		count, err := strconv.Atoi(args[0])
		if err != nil || count <= 0 {
			return fmt.Errorf("invalid rollback count %q: expected a positive decimal integer / 回滚数量必须为正十进制整数", args[0])
		}
		n = count
	}

	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	ctx := cmd.Context()

	runner, err := migrate.New(ctx, "migrations")
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer runner.Close()

	rolledBack, err := runner.Down(ctx, n)
	if err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}

	if len(rolledBack) == 0 {
		fmt.Printf("\n  %s✓ No migrations to rollback / 无可回滚的迁移%s\n\n", yellow, reset)
		return nil
	}

	fmt.Printf("\n  %s%s✓ %d migration(s) rolled back / 已回滚 %d 个迁移%s\n\n", bold, yellow, len(rolledBack), len(rolledBack), reset)
	for _, name := range rolledBack {
		fmt.Printf("  %s↩%s %s%s%s\n", yellow, reset, dim, name, reset)
	}
	fmt.Println()
	return nil
}

func runMigrateStatus(cmd *cobra.Command, args []string) error {
	green := "\033[32m"
	dim := "\033[2m"
	yellow := "\033[33m"
	reset := "\033[0m"

	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	ctx := cmd.Context()

	runner, err := migrate.New(ctx, "migrations")
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer runner.Close()

	statuses, err := runner.Status(ctx)
	if err != nil {
		return err
	}

	if len(statuses) == 0 {
		fmt.Println("  No migrations found / 未找到迁移文件")
		return nil
	}

	fmt.Println()
	for _, s := range statuses {
		if s.Applied {
			fmt.Printf("  %s✓%s %s %s%s%s\n", green, reset, s.Name, dim, s.AppliedAt.Format("2006-01-02 15:04:05"), reset)
		} else {
			fmt.Printf("  %s○%s %s %s(pending)%s\n", yellow, reset, s.Name, dim, reset)
		}
	}
	fmt.Println()
	return nil
}

// loadDialect returns the SQL dialect based on DATABASE_DRIVER env.
func loadDialect() (lux.Dialect, error) {
	driver, _ := env.Get("DATABASE_DRIVER")
	switch driver {
	case "", "pg", "postgres", "postgresql":
		return pg.Dialect{}, nil
	case "mysql", "sqlite", "mongo", "mongodb":
		return nil, fmt.Errorf("DATABASE_DRIVER %q is not implemented; currently supported: pg / 数据库驱动 %q 尚未实现；当前仅支持 pg", driver, driver)
	default:
		return nil, fmt.Errorf("unknown DATABASE_DRIVER %q; currently supported: pg / 未知数据库驱动 %q；当前仅支持 pg", driver, driver)
	}
}

// buildFieldIDMaps reads luxo.lock and the previous state to build rename detection maps.
func buildFieldIDMaps() map[string]*codegen.FieldIDMap {
	lf, err := lockfile.Load("luxo.lock")
	if err != nil {
		return nil
	}

	// Current lock has the latest field names → NewByID
	// Previous state (.state.json columns) has old names → we need old lock
	// Since luxo.lock is already updated by `luxo gen`, we use it as the new state.
	// For old state, we read .state.json column names as proxy.
	// Build from lock file: model → {fieldID → column name}
	newLock := make(map[string]map[string]int)
	for model, ml := range lf.Models {
		newLock[model] = ml.Fields
	}

	// Old lock: read from .state.json columns (they represent the DB state before this diff)
	oldLock := make(map[string]map[string]int)
	oldState, _ := codegen.LoadState(filepath.Join("migrations", ".state.json"))
	if oldState != nil {
		for model, ml := range lf.Models {
			// Map old column names to their field IDs
			if ms, ok := oldState.Models[model]; ok {
				fields := make(map[string]int)
				for fieldName, id := range ml.Fields {
					// Check if this field name existed as a column in old state
					if _, exists := ms.Columns[str.ToSnakeCase(fieldName)]; exists {
						fields[fieldName] = id
					}
				}
				oldLock[model] = fields
			}
		}
	}

	return codegen.BuildFieldIDMaps(oldLock, newLock)
}

func nextMigrationSeq() (int, error) {
	entries, err := os.ReadDir("migrations")
	if errors.Is(err, os.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read migration directory: %w", err)
	}
	highest, count := 0, 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		count++
		ordinal, err := migrationFileOrdinal(entry.Name())
		if err != nil {
			return 0, fmt.Errorf("migration sequence in %q: %w", entry.Name(), err)
		}
		highest = max(highest, ordinal)
	}
	highest = max(highest, count)
	if highest == int(^uint(0)>>1) {
		return 0, fmt.Errorf("migration sequence exhausted")
	}
	return highest + 1, nil
}

func migrationFileOrdinal(name string) (int, error) {
	first, rest, found := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
	if !found || !migrationOrdinalDigits(first) {
		return 0, nil
	}
	if len(first) == 8 {
		second, _, found := strings.Cut(rest, "_")
		if found && migrationOrdinalDigits(second) {
			first = second
		}
	}
	return strconv.Atoi(first)
}

func migrationOrdinalDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func runMigrateVerify(cmd *cobra.Command, args []string) error {
	green := "\033[32m"
	red := "\033[31m"
	bold := "\033[1m"
	reset := "\033[0m"

	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	ctx := cmd.Context()

	runner, err := migrate.New(ctx, "migrations")
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer runner.Close()

	drifts, err := runner.Verify(ctx)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}

	if len(drifts) == 0 {
		fmt.Printf("\n  %s%s✓ Database matches expected origin / 数据库与预期 origin 一致%s\n\n", bold, green, reset)
		return nil
	}

	fmt.Printf("\n  %s%s✗ Schema drift detected / 检测到 origin 漂移%s\n\n", bold, red, reset)
	for _, d := range drifts {
		fmt.Printf("  %s•%s %s\n", red, reset, d)
	}
	fmt.Println()
	return fmt.Errorf("%d drift(s) found", len(drifts))
}

func runMigrateSquash(cmd *cobra.Command, args []string) error {
	green := "\033[32m"
	bold := "\033[1m"
	dim := "\033[2m"
	reset := "\033[0m"

	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	dialect, err := loadDialect()
	if err != nil {
		return err
	}

	result, err := parseAndAnalyze()
	if err != nil {
		return err
	}

	enums := codegen.CollectEnumsFromResult(result)
	desired, err := codegen.ComputeState(result, enums, dialect)
	if err != nil {
		return fmt.Errorf("compute migration state: %w", err)
	}

	output := migrationOutput{
		fileName: fmt.Sprintf("%s_001_initial.sql", time.Now().Format("20060102")),
		header:   "-- Squashed migration: all tables from scratch\n-- Generated by luxo migrate squash\n\n",
	}
	path := filepath.Join("migrations", output.fileName)
	if err := checkMigrationOutputAvailable(path); err != nil {
		return err
	}
	ctx := context.Background()
	if cmd != nil {
		ctx = cmd.Context()
	}
	err = withMigrationGenerationLock(ctx, func() error {
		if err := checkMigrationOutputAvailable(path); err != nil {
			return err
		}
		var err error
		path, err = writeMigrationWithState(desired, func() (string, error) {
			return writeInitialMigration(desired, dialect, output)
		})
		return err
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n  %s%s✓ Migrations squashed / 迁移已合并%s\n\n", bold, green, reset)
	fmt.Printf("  %s+%s %s\n", green, reset, path)
	fmt.Printf("  %sRemove old migration files manually if desired%s\n", dim, reset)
	fmt.Printf("  %s如需删除旧迁移文件请手动操作%s\n\n", dim, reset)
	return nil
}

func checkMigrationOutputAvailable(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("migration output already exists: %s: %w", path, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
