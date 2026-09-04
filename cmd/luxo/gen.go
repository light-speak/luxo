package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
	luxenv "github.com/light-speak/luxo/pkg/lux/env"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/spf13/cobra"
)

var genCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate Go code from origin/*.luxo / 从 origin/*.luxo 生成 Go 代码",
	Long: `Parse all .luxo files in origin/ and generate Go code for each service module.
解析 origin/ 下的所有 .luxo 文件，为每个服务模块生成 Go 代码。

Generated files (*.gen.go) are written to service/<module>/luxo/.
生成的文件 (*.gen.go) 写入 service/<module>/luxo/。

These files are fully overwritten on each run — do not edit them.
这些文件每次运行时完全覆盖 — 请勿编辑。

Example / 示例:
  luxo gen`,
	Args: cobra.NoArgs,
	RunE: runGen,
}

var allowBreaking bool

func init() {
	genCmd.Flags().BoolVar(&allowBreaking, "allow-breaking", false, "Accept breaking wire-contract changes / 接受破坏性协议变更")
	rootCmd.AddCommand(genCmd)
}

func runGen(cmd *cobra.Command, args []string) error {
	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
	schemaFiles, err := findSchemaFiles()
	if err != nil {
		return err
	}

	files, err := parseAllFiles(schemaFiles)
	if err != nil {
		return err
	}

	result, err := analyzeFiles(files)
	if err != nil {
		return err
	}

	lf, err := updateLockFileAndReturn(files)
	if err != nil {
		return err
	}

	generator, err := buildProjectGenerator(lf, files)
	if err != nil {
		return err
	}

	totalFiles, err := generateModules(files, result, generator)
	if err != nil {
		return err
	}

	// Generate entry points
	modulePath, _ := readModulePath()
	if modulePath != "" {
		// Embedded entry: luxis/app/main.gen.go (all modules, single process)
		if err := generateEntry(result, modulePath); err != nil {
			return err
		}
		totalFiles++

		// Per-module entries: luxis/<module>/main.gen.go (cluster mode, one binary per module)
		moduleEntries, err := codegen.GenerateModuleEntryFilesChecked(result, modulePath)
		if err != nil {
			return err
		}
		for modName, src := range moduleEntries {
			outDir := filepath.Join("luxis", modName)
			if err := os.MkdirAll(outDir, 0755); err != nil {
				return fmt.Errorf("create %s: %w", outDir, err)
			}
			outPath := filepath.Join(outDir, "main.gen.go")
			if err := os.WriteFile(outPath, src, 0644); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}
			green := "\033[32m"
			reset := "\033[0m"
			fmt.Printf("  %s+%s %s\n", green, reset, outPath)
			totalFiles++
		}

		// Gateway entry: luxis/gateway/main.gen.go (pure router, no handler code)
		gwSrc, err := codegen.GenerateGatewayEntryChecked(result, modulePath)
		if err != nil {
			return err
		}
		if gwSrc != nil {
			outDir := filepath.Join("luxis", "gateway")
			if err := os.MkdirAll(outDir, 0755); err != nil {
				return fmt.Errorf("create %s: %w", outDir, err)
			}
			outPath := filepath.Join(outDir, "main.gen.go")
			if err := os.WriteFile(outPath, gwSrc, 0644); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}
			green := "\033[32m"
			reset := "\033[0m"
			fmt.Printf("  %s+%s %s\n", green, reset, outPath)
			totalFiles++
		}
	}

	// Export luxo.schema.json for SDK generation (vite plugin / dart / kotlin)
	if err := exportSchemaJSON(result, generator); err != nil {
		return fmt.Errorf("export schema: %w", err)
	}
	totalFiles++

	green := "\033[32m"
	dim := "\033[2m"
	bold := "\033[1m"
	reset := "\033[0m"
	fmt.Printf("\n%s%s✓ %d file(s) generated from %d origin(s)%s\n", bold, green, totalFiles, len(schemaFiles), reset)
	fmt.Printf("  %s%d 个 origin 生成了 %d 个文件%s\n\n", dim, len(schemaFiles), totalFiles, reset)
	return nil
}

func loadGenerationEnvironment(path string) error {
	if err := luxenv.Load(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load generation environment: %w", err)
	}
	return nil
}

func buildProjectGenerator(lf *lockfile.LockFile, files []*ast.File) (*codegen.GeneratorContext, error) {
	eventIDs := make(map[string]map[string]int, len(lf.Events))
	for name, event := range lf.Events {
		eventIDs[name] = event.Fields
	}
	modelIDs := make(map[string]map[string]int, len(lf.Models)+len(lf.Types))
	for name, model := range lf.Models {
		modelIDs[name] = model.Fields
	}
	for name, declaration := range lf.Types {
		modelIDs[name] = declaration.Fields
	}
	apiIDs := make(map[string]int, len(lf.APIs))
	paramIDs := make(map[string]map[string]int, len(lf.APIs))
	for name, api := range lf.APIs {
		apiIDs[name] = api.ID
		paramIDs[name] = api.Params
	}

	modulePath := goModulePath()
	if modulePath != "" {
		modulePath += "/service"
	}
	return codegen.NewGenerator(codegen.GeneratorConfig{
		Driver: codegen.DBDriver(os.Getenv("DATABASE_DRIVER")),
		IDs: codegen.StableIDs{
			EventFields:   eventIDs,
			ModelFields:   modelIDs,
			APIs:          apiIDs,
			APIParams:     paramIDs,
			APIParamTypes: buildParamTypesFromAST(files),
		},
		Events: codegen.BuildEventContext(files, modulePath),
	})
}

func findSchemaFiles() ([]string, error) {
	entries, err := os.ReadDir("origin")
	if err != nil {
		return nil, fmt.Errorf("read origin/: %w\n读取 origin/ 失败: %w", err, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".luxo") {
			// Single-file module: origin/user.luxo
			files = append(files, filepath.Join("origin", e.Name()))
		} else if e.IsDir() {
			// Directory module: origin/user/*.luxo
			subEntries, err := os.ReadDir(filepath.Join("origin", e.Name()))
			if err != nil {
				continue
			}
			for _, se := range subEntries {
				if !se.IsDir() && strings.HasSuffix(se.Name(), ".luxo") {
					files = append(files, filepath.Join("origin", e.Name(), se.Name()))
				}
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .luxo files found in origin/\norigin/ 下没有 .luxo 文件")
	}
	return files, nil
}

// groupByModule groups parsed files by module name.
// origin/user.luxo → module "user" with 1 file
// origin/user/model.luxo + origin/user/auth.luxo → module "user" with 2 merged files
func groupByModule(files []*ast.File) map[string][]*ast.File {
	modules := make(map[string][]*ast.File)
	for _, file := range files {
		// file.Name is the path like "origin/user/model.luxo" or "origin/user.luxo"
		rel := strings.TrimPrefix(file.Name, "origin/")
		parts := strings.Split(rel, "/")
		var moduleName string
		if len(parts) == 1 {
			// Single file: origin/user.luxo → module "user"
			moduleName = strings.TrimSuffix(parts[0], ".luxo")
		} else {
			// Directory: origin/user/model.luxo → module "user"
			moduleName = parts[0]
		}
		modules[moduleName] = append(modules[moduleName], file)
	}
	return modules
}

func parseAllFiles(paths []string) ([]*ast.File, error) {
	var files []*ast.File
	for _, path := range paths {
		file, err := parseFile(path)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func analyzeFiles(files []*ast.File) (*semantic.Result, error) {
	result := semantic.AnalyzeModules(files)
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "error: %s:%d: %s\n", e.Pos.File, e.Pos.Line, e.Message)
		}
		return nil, fmt.Errorf("%d error(s) / %d 个错误", len(result.Errors), len(result.Errors))
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s:%d: %s\n", w.Pos.File, w.Pos.Line, w.Message)
	}
	return result, nil
}

func updateLockFileAndReturn(files []*ast.File) (*lockfile.LockFile, error) {
	green := "\033[32m"
	reset := "\033[0m"
	lf, err := lockfile.Load("luxo.lock")
	if err != nil {
		return nil, fmt.Errorf("load luxo.lock: %w\n加载 luxo.lock 失败: %w", err, err)
	}
	if err := validateWireCompatibility(lf, files, allowBreaking); err != nil {
		return nil, err
	}
	lf.Update(files)
	if err := lf.Save("luxo.lock"); err != nil {
		return nil, fmt.Errorf("save luxo.lock: %w\n保存 luxo.lock 失败: %w", err, err)
	}
	fmt.Printf("  %s✓%s luxo.lock\n", green, reset)
	return lf, nil
}

func validateWireCompatibility(lf *lockfile.LockFile, files []*ast.File, allow bool) error {
	changes := lf.BreakingChanges(files)
	if len(changes) == 0 || allow {
		return nil
	}
	var message strings.Builder
	message.WriteString("breaking binary contract changes detected / 检测到破坏性二进制协议变更:\n")
	for _, change := range changes {
		fmt.Fprintf(&message, "  - %s\n", change)
	}
	message.WriteString("rerun with --allow-breaking only after coordinating every consumer / 仅在所有消费端协调升级后使用 --allow-breaking")
	return fmt.Errorf("%s", message.String())
}

// buildParamTypesFromAST extracts param types from AST for accurate binary metadata.
func buildParamTypesFromAST(files []*ast.File) map[string]map[string]string {
	types := make(map[string]map[string]string)
	enums := make(map[string]bool)
	for _, file := range files {
		for _, enum := range file.Enums {
			enums[enum.Name] = true
		}
	}
	for _, file := range files {
		for _, a := range file.APIs {
			if len(a.Params) == 0 {
				continue
			}
			m := make(map[string]string, len(a.Params))
			for _, p := range a.Params {
				if p.Type != nil {
					m[p.Name] = binaryParamType(p.Type, enums)
				}
			}
			types[a.Name] = m
		}
		for _, fn := range file.Functions {
			if !hasASTDirective(fn.Directives, "service") || len(fn.Params) == 0 {
				continue
			}
			m := make(map[string]string, len(fn.Params))
			for _, p := range fn.Params {
				if p.Type != nil {
					m[p.Name] = binaryParamType(p.Type, enums)
				}
			}
			types[fn.Name] = m
		}
		for _, model := range file.Models {
			hasCrud := false
			for _, d := range model.Directives {
				if d.Name == "crud" {
					hasCrud = true
					break
				}
			}
			if !hasCrud {
				continue
			}
			idType := "Int"
			for _, f := range model.Fields {
				if f.Name == "id" && f.Type != nil {
					idType = binaryParamType(&ast.TypeRef{Name: f.Type.Name}, enums)
					break
				}
			}
			createTypes := make(map[string]string)
			updateTypes := map[string]string{"id": idType}
			for _, f := range model.Fields {
				if !isGeneratedCRUDParam(f, enums, false) {
					continue
				}
				createTypes[f.Name] = binaryParamType(f.Type, enums)
				if isGeneratedCRUDParam(f, enums, true) {
					updateTypes[f.Name] = binaryParamType(f.Type, enums)
				}
			}
			name := model.Name
			plural := name + "s"
			types["get"+name] = map[string]string{"id": idType}
			types["delete"+name] = map[string]string{"id": idType}
			types["delete"+plural] = map[string]string{"ids": "[" + idType + "]"}
			types["list"+plural] = map[string]string{"page": "Int", "pageSize": "Int"}
			types["create"+name] = createTypes
			types["update"+name] = updateTypes
		}
	}
	return types
}

func isGeneratedCRUDParam(field *ast.FieldDecl, enums map[string]bool, update bool) bool {
	if field.Type == nil || field.Computed != nil || hasASTDirective(field.Directives, "internal") ||
		hasASTDirective(field.Directives, "serial") || hasASTDirective(field.Directives, "auto") {
		return false
	}
	if field.Type.Name == "DateTime" && (field.Name == "createdAt" || field.Name == "updatedAt") {
		return false
	}
	if update && (field.Name == "id" || hasASTDirective(field.Directives, "immutable")) {
		return false
	}
	if !isBinaryScalarParamType(field.Type.Name) && !enums[field.Type.Name] {
		return false
	}
	return true
}

func binaryParamType(ref *ast.TypeRef, enums map[string]bool) string {
	typeName := ref.Name
	if enums[typeName] {
		typeName = "Enum"
	} else if !isBinaryScalarParamType(typeName) {
		typeName = "JSON"
	}
	if ref.IsList {
		typeName = "[" + typeName + "]"
	}
	if ref.Nullable {
		typeName += "?"
	}
	return typeName
}

func isBinaryScalarParamType(typeName string) bool {
	switch typeName {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "Bytes", "UUID", "Decimal", "JSON":
		return true
	}
	return false
}

func hasASTDirective(directives []*ast.Directive, name string) bool {
	for _, directive := range directives {
		if directive.Name == name {
			return true
		}
	}
	return false
}

func generateModules(files []*ast.File, result *semantic.Result, generator *codegen.GeneratorContext) (int, error) {
	green := "\033[32m"
	reset := "\033[0m"

	// Collect @soft model names across ALL modules (for cross-module DataLoader filtering)
	softModels := make(map[string]bool)
	for _, file := range files {
		for _, m := range file.Models {
			if codegen.IsSoftDelete(m) {
				softModels[m.Name] = true
			}
		}
	}
	// Group files by module — directory name or file name without .luxo
	modules := groupByModule(files)

	total := 0
	for moduleName, moduleFiles := range modules {
		outDir := filepath.Join("service", moduleName, "luxo")
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return 0, fmt.Errorf("create %s: %w", outDir, err)
		}
		singleResult := &semantic.Result{Files: moduleFiles}
		gr, err := generator.Generate(singleResult, "luxo", softModels)
		if err != nil {
			return 0, fmt.Errorf("generate module %s: %w", moduleName, err)
		}
		for name, src := range gr.Files {
			outPath := filepath.Join(outDir, name)
			if err := os.WriteFile(outPath, src, 0644); err != nil {
				return 0, fmt.Errorf("write %s: %w", outPath, err)
			}
			fmt.Printf("  %s+%s %s\n", green, reset, outPath)
			total++
		}

		// Remove stale .gen.go files whose declarations were removed from the
		// origin — a generator returning nil no longer overwrites its old output.
		removed, err := cleanStaleGenFiles(outDir, gr.Files)
		if err != nil {
			return 0, err
		}
		for _, name := range removed {
			red := "\033[31m"
			fmt.Printf("  %s-%s %s\n", red, reset, filepath.Join(outDir, name))
		}

		// Auto-create resolver package if it doesn't exist
		resolverDir := filepath.Join("service", moduleName, "resolver")
		resolverFile := filepath.Join(resolverDir, "resolver.go")
		if _, err := os.Stat(resolverFile); os.IsNotExist(err) {
			if err := os.MkdirAll(resolverDir, 0755); err != nil {
				return 0, fmt.Errorf("create %s: %w", resolverDir, err)
			}
			// Read go.mod to get module path
			modPath, _ := readModulePath()
			resolverSrc := fmt.Sprintf("package resolver\n\nimport luxo %q\n\n// Setup registers @native resolvers.\nfunc Setup(app *luxo.App) {}\n",
				modPath+"/service/"+moduleName+"/luxo")
			if err := os.WriteFile(resolverFile, []byte(resolverSrc), 0644); err != nil {
				return 0, fmt.Errorf("write %s: %w", resolverFile, err)
			}
			fmt.Printf("  %s+%s %s\n", green, reset, resolverFile)
		}
	}
	return total, nil
}

// cleanStaleGenFiles deletes *.gen.go files in dir that are not part of the
// current generation run (their origin declarations were removed). Handwritten
// files are never touched — only the .gen.go suffix is eligible.
// Returns the removed file names.
func cleanStaleGenFiles(dir string, written map[string][]byte) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var removed []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".gen.go") {
			continue
		}
		if _, ok := written[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return nil, fmt.Errorf("remove stale %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func goModulePath() string {
	p, _ := readModulePath()
	return p
}

func generateEntry(result *semantic.Result, modulePath string) error {
	green := "\033[32m"
	reset := "\033[0m"
	src, err := codegen.GenerateEntryFileChecked(result, modulePath)
	if err != nil {
		return fmt.Errorf("generate embedded entry: %w", err)
	}
	if src == nil {
		return nil
	}
	outDir := filepath.Join("luxis", "app")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, "main.gen.go")
	if err := os.WriteFile(outPath, src, 0644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("  %s+%s %s\n", green, reset, outPath)
	return nil
}

func exportSchemaJSON(result *semantic.Result, generator *codegen.GeneratorContext) error {
	enums := codegen.CollectEnumsFromResult(result)
	data, err := generator.BuildSchemaJSON(result, enums)
	if err != nil {
		return err
	}
	if err := os.WriteFile("luxo.schema.json", data, 0644); err != nil {
		return err
	}
	green := "\033[32m"
	reset := "\033[0m"
	fmt.Printf("  %s✓%s luxo.schema.json\n", green, reset)
	return nil
}

func parseFile(path string) (*ast.File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	l := lexer.New(string(content), path)
	tokens, lexErrs := l.Tokenize()
	if len(lexErrs) > 0 {
		for _, e := range lexErrs {
			fmt.Fprintf(os.Stderr, "error: %s\n", e)
		}
		return nil, fmt.Errorf("%d lexer error(s) in %s", len(lexErrs), path)
	}

	p := parser.New(tokens)
	file, parseErrs := p.Parse(path)
	if len(parseErrs) > 0 {
		for _, e := range parseErrs {
			fmt.Fprintf(os.Stderr, "error: %s\n", e)
		}
		return nil, fmt.Errorf("%d parser error(s) in %s", len(parseErrs), path)
	}

	return file, nil
}
