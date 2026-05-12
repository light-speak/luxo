package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
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

func init() {
	rootCmd.AddCommand(genCmd)
}

func runGen(cmd *cobra.Command, args []string) error {
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

	// Pass field IDs from lock file to codegen for binary encoding
	if lf.Events != nil {
		ids := make(map[string]map[string]int, len(lf.Events))
		for name, el := range lf.Events {
			ids[name] = el.Fields
		}
		codegen.SetEventFieldIDs(ids)
	}
	if lf.Models != nil || lf.Types != nil {
		ids := make(map[string]map[string]int, len(lf.Models)+len(lf.Types))
		for name, ml := range lf.Models {
			ids[name] = ml.Fields
		}
		// Types have independent ID space but codegen uses the same lookup
		for name, tl := range lf.Types {
			ids[name] = tl.Fields
		}
		codegen.SetModelFieldIDs(ids)
	}
	if lf.APIs != nil {
		ids := make(map[string]int, len(lf.APIs))
		paramIDs := make(map[string]map[string]int, len(lf.APIs))
		for name, al := range lf.APIs {
			ids[name] = al.ID
			if len(al.Params) > 0 {
				paramIDs[name] = al.Params
			}
		}
		codegen.SetAPIIDs(ids)
		codegen.SetAPIParamIDs(paramIDs)
	}

	// Extract param types from AST for accurate binary metadata
	paramTypes := buildParamTypesFromAST(files)
	if len(paramTypes) > 0 {
		codegen.SetAPIParamTypes(paramTypes)
	}

	totalFiles, err := generateModules(files, result)
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
		moduleEntries := codegen.GenerateModuleEntryFiles(result, modulePath)
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
		if gwSrc := codegen.GenerateGatewayEntry(result, modulePath); gwSrc != nil {
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
	if err := exportSchemaJSON(result); err != nil {
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
	a := semantic.New()
	result := a.Analyze(files)
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
	lf.Update(files)
	if err := lf.Save("luxo.lock"); err != nil {
		return nil, fmt.Errorf("save luxo.lock: %w\n保存 luxo.lock 失败: %w", err, err)
	}
	fmt.Printf("  %s✓%s luxo.lock\n", green, reset)
	return lf, nil
}

// buildParamTypesFromAST extracts param types from AST for accurate binary metadata.
func buildParamTypesFromAST(files []*ast.File) map[string]map[string]string {
	types := make(map[string]map[string]string)
	for _, file := range files {
		for _, a := range file.APIs {
			if len(a.Params) == 0 {
				continue
			}
			m := make(map[string]string, len(a.Params))
			for _, p := range a.Params {
				if p.Type != nil {
					m[p.Name] = p.Type.Name
				}
			}
			types[a.Name] = m
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
			fieldTypes := make(map[string]string)
			for _, f := range model.Fields {
				if f.Type != nil && f.Computed == nil {
					fieldTypes[f.Name] = f.Type.Name
				}
			}
			name := model.Name
			plural := name + "s"
			idType := fieldTypes["id"]
			if idType == "" {
				idType = "Int"
			}
			types["get"+name] = map[string]string{"id": idType}
			types["delete"+name] = map[string]string{"id": idType}
			types["delete"+plural] = map[string]string{"ids": idType}
			types["list"+plural] = map[string]string{"page": "Int", "pageSize": "Int"}
			createTypes := make(map[string]string)
			for k, v := range fieldTypes {
				createTypes[k] = v
			}
			types["create"+name] = createTypes
			updateTypes := map[string]string{"id": idType}
			for k, v := range createTypes {
				updateTypes[k] = v
			}
			types["update"+name] = updateTypes
		}
	}
	return types
}

func generateModules(files []*ast.File, result *semantic.Result) (int, error) {
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

	// Build cross-module event context — needed for emit/on across modules
	modulePath := goModulePath()
	if modulePath != "" {
		modulePath += "/service"
	}
	evCtx := codegen.BuildEventContext(files, modulePath)
	codegen.SetEventContext(evCtx)

	total := 0
	for moduleName, moduleFiles := range modules {
		outDir := filepath.Join("service", moduleName, "luxo")
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return 0, fmt.Errorf("create %s: %w", outDir, err)
		}
		singleResult := &semantic.Result{Files: moduleFiles}
		driver := codegen.DBDriver(os.Getenv("DATABASE_DRIVER"))
		if driver == "" {
			driver = codegen.DriverPG
		}
		gr := codegen.Generate(singleResult, "luxo", driver, softModels)
		for name, src := range gr.Files {
			outPath := filepath.Join(outDir, name)
			if err := os.WriteFile(outPath, src, 0644); err != nil {
				return 0, fmt.Errorf("write %s: %w", outPath, err)
			}
			fmt.Printf("  %s+%s %s\n", green, reset, outPath)
			total++
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

func goModulePath() string {
	p, _ := readModulePath()
	return p
}

func generateEntry(result *semantic.Result, modulePath string) error {
	green := "\033[32m"
	reset := "\033[0m"
	src := codegen.GenerateEntryFile(result, modulePath)
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

func exportSchemaJSON(result *semantic.Result) error {
	enums := codegen.CollectEnumsFromResult(result)
	data, err := codegen.BuildSchemaJSON(result, enums)
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
