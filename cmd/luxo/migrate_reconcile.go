package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
	"github.com/spf13/cobra"
)

func newMigrateReconcileCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "reconcile [attempt-id]",
		Short: "Inspect or resolve a migration attempt / 查看或处理迁移尝试",
		Long: `Read the pending migration attempt with no arguments.
After inspecting the affected database objects, pass the attempt ID and one action.

For --record-executed or --retry, pause conflicting DDL from clients that do not
acquire Luxo's PostgreSQL migration advisory lock. Keep it paused from the start
of inspection until the action completes.

不带参数时只读查看待处理的迁移尝试。
检查受影响的数据库对象后，传入尝试 ID 和一项处理操作。

使用 --record-executed 或 --retry 时，从开始检查到操作结束，
须暂停其他未获取 Luxo PostgreSQL 迁移咨询锁的客户端所执行的冲突 DDL。`,
		Args: cobra.MaximumNArgs(1),
	}
	options := [...]struct {
		name, help string
		action     migrate.ReconciliationAction
	}{
		{"record-executed", "Record a verified SQL result in migration history / 将已核实的 SQL 结果写入迁移历史", migrate.RecordApplied},
		{"retry", "Run the recorded standalone SQL again / 重新执行记录的独立 SQL", migrate.RetryMigration},
		{"acknowledge", "Clear a receipt after migration history has committed / 清除迁移历史已提交的回执", migrate.AcknowledgeMigration},
	}
	var names []string
	for _, option := range options {
		command.Flags().Bool(option.name, false, option.help)
		names = append(names, option.name)
	}
	command.MarkFlagsMutuallyExclusive(names...)
	command.RunE = func(cmd *cobra.Command, args []string) error {
		for _, option := range options {
			flag := cmd.Flags().Lookup(option.name)
			if !flag.Changed {
				continue
			}
			if len(args) == 0 || args[0] == "" || strings.TrimSpace(args[0]) != args[0] {
				return fmt.Errorf("--%s requires an attempt ID without surrounding whitespace / 操作需要无首尾空白的尝试 ID", option.name)
			}
			if flag.Value.String() != "true" {
				return fmt.Errorf("--%s must be true to select the action / 必须显式启用操作", option.name)
			}
			return runMigrateReconcile(cmd, args[0], option.action)
		}
		if len(args) != 0 {
			return errors.New("choose exactly one of --record-executed, --retry, or --acknowledge / 请指定一项操作")
		}
		return runMigrateReconcile(cmd, "", 0)
	}
	return command
}

func runMigrateReconcile(cmd *cobra.Command, id string, action migrate.ReconciliationAction) error {
	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	ctx := cmd.Context()
	runner, err := migrate.New(ctx, "migrations")
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer runner.Close()

	if id == "" {
		return displayPendingMigrationAttempt(cmd, runner)
	}
	if err := runner.Reconcile(ctx, id, action); err != nil {
		return fmt.Errorf("migrate reconcile: %w", err)
	}
	if action == migrate.AcknowledgeMigration {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Migration receipt acknowledged / 已确认迁移回执 %q\n", id)
	} else {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Recovery action completed / 迁移处理操作已完成 %q\n"+
			"Run luxo migrate reconcile to check for a committed receipt / 运行 luxo migrate reconcile 查看是否有已提交的回执\n", id)
	}
	return err
}

func displayPendingMigrationAttempt(cmd *cobra.Command, runner *migrate.Runner) error {
	attempt, err := runner.PendingAttempt(cmd.Context())
	if err != nil {
		return fmt.Errorf("migrate reconcile: %w", err)
	}
	if attempt == nil {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No migration attempt awaits reconciliation / 无待处理的迁移尝试")
		return err
	}
	advice := "Inspect the affected objects, then choose --record-executed or --retry / 检查受影响的数据库对象后，选择记录或重试"
	if attempt.HistoryCommitted {
		advice = "Use --acknowledge with this attempt ID / 使用此尝试 ID 确认回执"
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Attempt ID / 尝试 ID: %q\nMigration / 迁移: %s %q\nHistory committed / 历史已提交: %t\n%s\n",
		attempt.ID, attempt.Direction, attempt.Name, attempt.HistoryCommitted, advice)
	return err
}
