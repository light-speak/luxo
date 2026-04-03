package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// Server is the Luxo Language Server.
type Server struct {
	transport *Transport
	docs      *DocumentStore
	logger    *log.Logger
	shutdown  bool
}

// NewServer creates a new LSP server.
func NewServer(reader io.Reader, writer io.Writer, logger *log.Logger) *Server {
	return &Server{
		transport: NewTransport(reader, writer),
		docs:      NewDocumentStore(),
		logger:    logger,
	}
}

// Run starts the server main loop.
func (s *Server) Run() error {
	s.logger.Println("luxo-lsp: starting")

	for {
		req, err := s.transport.ReadMessage()
		if err != nil {
			if s.shutdown {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		s.logger.Printf("← %s", req.Method)

		if err := s.handleMessage(req); err != nil {
			s.logger.Printf("error in %s: %v", req.Method, err)
		}

		if req.Method == "exit" {
			return nil
		}
	}
}

func (s *Server) handleMessage(req *Request) error {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		return nil
	case "shutdown":
		s.shutdown = true
		return s.transport.SendResponse(req.ID, nil)
	case "exit":
		return nil
	case "textDocument/didOpen":
		return s.handleDidOpen(req)
	case "textDocument/didChange":
		return s.handleDidChange(req)
	case "textDocument/didClose":
		return s.handleDidClose(req)
	case "textDocument/didSave":
		return nil
	case "textDocument/completion":
		return s.handleCompletion(req)
	case "textDocument/hover":
		return s.handleHover(req)
	case "textDocument/definition":
		return s.handleDefinition(req)
	default:
		if req.ID != nil {
			return s.transport.SendError(req.ID, -32601, "method not found: "+req.Method)
		}
		return nil
	}
}

// ========== Initialize ==========

func (s *Server) handleInitialize(req *Request) error {
	return s.transport.SendResponse(req.ID, InitializeResult{
		Capabilities: ServerCapabilities{
			TextDocumentSync: 1, // Full
			CompletionProvider: &CompletionOpt{
				TriggerCharacters: []string{".", "@", ":"},
			},
			HoverProvider:      true,
			DefinitionProvider: true,
		},
		ServerInfo: ServerInfo{
			Name:    "luxo-lsp",
			Version: "0.2.0",
		},
	})
}

// ========== Document Sync ==========

func (s *Server) handleDidOpen(req *Request) error {
	var params DidOpenParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	s.logger.Printf("open: %s (%d bytes)", params.TextDocument.URI, len(params.TextDocument.Text))
	doc := s.docs.Open(params.TextDocument.URI, params.TextDocument.Version, params.TextDocument.Text)
	return s.publishDiagnostics(doc)
}

func (s *Server) handleDidChange(req *Request) error {
	var params DidChangeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	if len(params.ContentChanges) == 0 {
		return nil
	}

	content := params.ContentChanges[len(params.ContentChanges)-1].Text
	doc := s.docs.Update(params.TextDocument.URI, params.TextDocument.Version, content)
	return s.publishDiagnostics(doc)
}

func (s *Server) handleDidClose(req *Request) error {
	var params DidCloseParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	s.docs.Close(params.TextDocument.URI)
	return s.transport.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []Diagnostic{},
	})
}

func (s *Server) publishDiagnostics(doc *Document) error {
	if doc == nil {
		return nil
	}
	diags := doc.Diagnostics()
	if diags == nil {
		diags = []Diagnostic{}
	}
	s.logger.Printf("→ diagnostics: %d items for %s", len(diags), doc.URI)
	return s.transport.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         doc.URI,
		Diagnostics: diags,
	})
}

// ========== Completion ==========

func (s *Server) handleCompletion(req *Request) error {
	var params CompletionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	doc := s.docs.Get(params.TextDocument.URI)
	if doc == nil {
		return s.transport.SendResponse(req.ID, CompletionList{})
	}

	items := s.getCompletions(doc, params.Position)
	return s.transport.SendResponse(req.ID, CompletionList{Items: items})
}

func (s *Server) getCompletions(doc *Document, pos Position) []CompletionItem {
	var items []CompletionItem

	prevChar := doc.PrevChar(pos)
	word := doc.WordAt(pos)
	prefix := strings.ToLower(word)

	// after '.' — field/method completion
	if prevChar == '.' {
		items = append(items, s.getMemberCompletions(doc, pos)...)
		return items
	}

	// after '@' — directive completion
	if prevChar == '@' || strings.HasPrefix(word, "@") {
		items = append(items, getDirectiveCompletions()...)
		return items
	}

	// keyword completions
	items = append(items, getKeywordCompletions(prefix)...)

	// type and symbol completions from analysis
	if doc.Result != nil && doc.Result.Scope != nil {
		items = append(items, s.getSymbolCompletions(doc, prefix)...)
	}

	// built-in type completions
	items = append(items, getBuiltinTypeCompletions(prefix)...)

	return items
}

func (s *Server) getMemberCompletions(doc *Document, pos Position) []CompletionItem {
	var items []CompletionItem
	if doc.Result == nil {
		return items
	}

	// find the word before the dot
	lines := strings.Split(doc.Content, "\n")
	if pos.Line >= len(lines) {
		return items
	}
	line := lines[pos.Line]

	// find the object name before '.'
	dotPos := pos.Character - 1
	if dotPos < 0 || dotPos >= len(line) || line[dotPos] != '.' {
		return items
	}

	end := dotPos
	start := end - 1
	for start >= 0 && isIdentChar(line[start]) {
		start--
	}
	start++
	if start >= end {
		return items
	}
	objName := line[start:end]

	// look up the type
	if typ, ok := doc.Result.Types[objName]; ok {
		// type member access: Role.USER, Role.ADMIN
		if typ.Kind == semantic.TypeEnum {
			for _, v := range typ.EnumValues {
				items = append(items, CompletionItem{
					Label:  v,
					Kind:   20, // EnumMember
					Detail: objName + "." + v,
				})
			}
		}
		// model field access
		for name, field := range typ.Fields {
			detail := field.Type.Name
			if field.Nullable {
				detail += "?"
			}
			items = append(items, CompletionItem{
				Label:  name,
				Kind:   5, // Field
				Detail: detail,
			})
		}
		// inherited fields
		for _, parent := range typ.Parents {
			for name, field := range parent.Fields {
				detail := field.Type.Name + " (from " + parent.Name + ")"
				items = append(items, CompletionItem{
					Label:  name,
					Kind:   5,
					Detail: detail,
				})
			}
		}
		return items
	}

	// variable member access: look up variable type
	sym := doc.Result.Scope.Lookup(objName)
	if sym != nil && sym.Type != nil {
		for name, field := range sym.Type.Fields {
			detail := ""
			if field.Type != nil {
				detail = field.Type.Name
			}
			items = append(items, CompletionItem{
				Label:  name,
				Kind:   5,
				Detail: detail,
			})
		}
	}

	// collection methods
	collectionMethods := []struct {
		name   string
		detail string
	}{
		{"filter", "filter { condition }"},
		{"map", "map { transform }"},
		{"sumOf", "sumOf { expr }"},
		{"count", "count { condition }"},
		{"any", "any { condition }"},
		{"firstOrNull", "firstOrNull { condition }"},
		{"sortBy", "sortBy { field }"},
		{"groupBy", "groupBy { field }"},
		{"forEach", "forEach { action }"},
		{"size", "collection size"},
		{"length", "string length"},
		{"contains", "contains(value)"},
		{"lowercase", "to lowercase"},
		{"uppercase", "to uppercase"},
	}
	for _, m := range collectionMethods {
		items = append(items, CompletionItem{
			Label:  m.name,
			Kind:   2, // Method
			Detail: m.detail,
		})
	}

	return items
}

func (s *Server) getSymbolCompletions(doc *Document, prefix string) []CompletionItem {
	var items []CompletionItem
	seen := map[string]bool{}

	symbols := doc.Result.Scope.LookupPrefix(prefix)
	for _, sym := range symbols {
		if seen[sym.Name] {
			continue
		}
		seen[sym.Name] = true

		kind := symbolKindToCompletionKind(sym.Kind)
		detail := sym.Kind.String()
		if sym.Type != nil {
			detail += ": " + sym.Type.Name
		}

		items = append(items, CompletionItem{
			Label:         sym.Name,
			Kind:          kind,
			Detail:        detail,
			Documentation: sym.Doc,
		})
	}
	return items
}

func symbolKindToCompletionKind(kind semantic.SymbolKind) int {
	switch kind {
	case semantic.SymModel:
		return 7 // Class
	case semantic.SymInterface:
		return 8 // Interface
	case semantic.SymEnum:
		return 13 // Enum
	case semantic.SymSealed:
		return 7 // Class
	case semantic.SymType:
		return 7 // Class
	case semantic.SymApi:
		return 3 // Function
	case semantic.SymFn:
		return 3 // Function
	case semantic.SymError:
		return 7 // Class
	case semantic.SymVariable:
		return 6 // Variable
	case semantic.SymParam:
		return 6 // Variable
	default:
		return 1 // Text
	}
}

func getDirectiveCompletions() []CompletionItem {
	directives := []struct {
		name, detail string
	}{
		{"id", "primary key"}, {"unique", "unique constraint"}, {"index", "database index"},
		{"varchar", "string length"}, {"hidden", "never return to client"}, {"hash", "auto bcrypt hash"},
		{"immutable", "cannot change after create"}, {"internal", "only set internally"},
		{"visible", "conditional visibility"}, {"transform", "transform on return"},
		{"beforeSave", "transform before save"}, {"mask", "mask sensitive data"},
		{"filterable", "enable filtering"}, {"sortable", "enable sorting"},
		{"search", "full-text search"}, {"crud", "auto generate CRUD"},
		{"auth", "require authentication"}, {"native", "native code implementation"},
		{"cache", "cache result"}, {"rateLimit", "rate limiting"}, {"scope", "query preset"},
		{"deprecated", "mark as deprecated"}, {"count", "aggregate count"},
		{"sum", "aggregate sum"}, {"avg", "aggregate average"},
		{"min", "aggregate min"}, {"max", "aggregate max"},
		{"cron", "scheduled task"}, {"retry", "retry on failure"},
		{"email", "email validation"}, {"url", "URL validation"},
		{"pattern", "regex validation"}, {"minLength", "minimum length"},
		{"maxLength", "maximum length"}, {"range", "numeric range"},
		{"notBlank", "not blank"}, {"webhook", "webhook endpoint"},
	}

	items := make([]CompletionItem, 0, len(directives))
	for _, d := range directives {
		items = append(items, CompletionItem{
			Label:  d.name,
			Kind:   14,
			Detail: "@" + d.name + " — " + d.detail,
		})
	}
	return items
}

func getKeywordCompletions(prefix string) []CompletionItem {
	keywords := []struct {
		label, detail, snippet string
	}{
		{"model", "model declaration", "model ${1:Name} {\n\t$0\n}"},
		{"interface", "interface declaration", "interface ${1:Name} {\n\t$0\n}"},
		{"enum", "enum declaration", "enum ${1:Name} {\n\t$0\n}"},
		{"sealed", "sealed type", "sealed ${1:Name} {\n\t$0\n}"},
		{"type", "type declaration", "type ${1:Name} {\n\t$0\n}"},
		{"api", "api endpoint", "api ${1:name}(${2:params}): ${3:Type} {\n\t$0\n}"},
		{"fn", "function", "fn ${1:name}(${2:params}): ${3:Type} {\n\t$0\n}"},
		{"error", "error declaration", "error ${1:Name} {\n\tcode: ${2:400}\n\tmessage: error.${3:name}\n}"},
		{"extend", "extend type", "extend ${1:Type} {\n\t$0\n}"},
		{"middleware", "middleware", "middleware ${1:name} {\n\t$0\n}"},
		{"val", "variable", "val ${1:name} = $0"},
		{"when", "when expression", "when {\n\t$0\n}"},
		{"if", "if statement", "if ${1:condition} {\n\t$0\n}"},
		{"for", "for loop", "for ${1:item} in ${2:collection} {\n\t$0\n}"},
		{"return", "return", "return $0"},
		{"throw", "throw error", "throw error.$0"},
		{"emit", "emit event", "emit(\"$1\", $0)"},
		{"transaction", "transaction block", "transaction {\n\t$0\n}"},
		{"find", "find records", "find(${1:Model}, ${2:where: condition})"},
		{"create", "create record", "create(${1:Model}, ${2:field: value})"},
		{"update", "update record", "update(${1:record}, ${2:field: value})"},
		{"delete", "delete record", "delete(${1:record})"},
		{"use", "import module", "use ${1:module}.{ $0 }"},
		{"override", "override api", "override api $0"},
		{"true", "boolean true", ""},
		{"false", "boolean false", ""},
		{"null", "null value", ""},
	}

	var items []CompletionItem
	for _, kw := range keywords {
		if prefix == "" || strings.HasPrefix(kw.label, prefix) {
			item := CompletionItem{
				Label:  kw.label,
				Kind:   14, // Keyword
				Detail: kw.detail,
			}
			if kw.snippet != "" {
				item.InsertText = kw.snippet
				item.Kind = 15 // Snippet
			}
			items = append(items, item)
		}
	}
	return items
}

func getBuiltinTypeCompletions(prefix string) []CompletionItem {
	types := []string{"String", "Int", "Float", "Boolean", "DateTime", "Duration", "Json", "File"}
	var items []CompletionItem
	for _, t := range types {
		if prefix == "" || strings.HasPrefix(strings.ToLower(t), prefix) {
			items = append(items, CompletionItem{
				Label:  t,
				Kind:   7,
				Detail: "built-in type",
			})
		}
	}
	return items
}

// ========== Hover ==========

func (s *Server) handleHover(req *Request) error {
	var params HoverParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	doc := s.docs.Get(params.TextDocument.URI)
	if doc == nil || doc.Result == nil {
		return s.transport.SendResponse(req.ID, nil)
	}

	word := doc.WordAt(params.Position)
	if word == "" {
		return s.transport.SendResponse(req.ID, nil)
	}

	// type hover
	if typ, ok := doc.Result.Types[word]; ok {
		return s.transport.SendResponse(req.ID, Hover{
			Contents: MarkupContent{Kind: "markdown", Value: formatTypeHover(word, typ)},
		})
	}

	// symbol hover
	if sym := doc.Result.Scope.Lookup(word); sym != nil {
		return s.transport.SendResponse(req.ID, Hover{
			Contents: MarkupContent{Kind: "markdown", Value: formatSymbolHover(sym)},
		})
	}

	// keyword hover
	if desc := keywordDescription(word); desc != "" {
		return s.transport.SendResponse(req.ID, Hover{
			Contents: MarkupContent{Kind: "markdown", Value: desc},
		})
	}

	return s.transport.SendResponse(req.ID, nil)
}

func formatTypeHover(name string, typ *semantic.ResolvedType) string {
	var b strings.Builder
	b.WriteString("```luxo\n")

	switch typ.Kind {
	case semantic.TypeModel:
		b.WriteString("model " + name)
		if len(typ.Parents) > 0 {
			b.WriteString(" : ")
			for i, p := range typ.Parents {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(p.Name)
			}
		}
		b.WriteString(" {\n")
		for fieldName, field := range typ.Fields {
			b.WriteString("  " + fieldName + ": ")
			if field.Type != nil {
				b.WriteString(field.Type.Name)
			}
			if field.Nullable {
				b.WriteString("?")
			}
			b.WriteString("\n")
		}
		b.WriteString("}")

	case semantic.TypeEnum:
		b.WriteString("enum " + name + " {\n")
		for _, v := range typ.EnumValues {
			b.WriteString("  " + v + "\n")
		}
		b.WriteString("}")

	case semantic.TypeSealed:
		b.WriteString("sealed " + name + " {\n")
		for _, v := range typ.Variants {
			b.WriteString("  " + v.Name)
			if len(v.Fields) > 0 {
				b.WriteString("(")
				for i, f := range v.Fields {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(f.Name)
				}
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		b.WriteString("}")

	default:
		b.WriteString("type " + name)
	}

	b.WriteString("\n```")
	return b.String()
}

func formatSymbolHover(sym *semantic.Symbol) string {
	var b strings.Builder
	b.WriteString("```luxo\n")
	b.WriteString(sym.Kind.String() + " " + sym.Name)
	if sym.Type != nil {
		b.WriteString(": " + sym.Type.Name)
	}
	b.WriteString("\n```")
	if sym.Doc != "" {
		b.WriteString("\n\n---\n\n" + sym.Doc)
	}
	return b.String()
}

func keywordDescription(word string) string {
	descriptions := map[string]string{
		"model":       "`model` — Define a data model, maps to database table\n\n定义数据模型，映射到数据库表",
		"interface":   "`interface` — Define an interface with optional default implementations\n\n定义接口，可带默认实现",
		"enum":        "`enum` — Define an enumeration type\n\n定义枚举类型",
		"sealed":      "`sealed` — Define a sealed type, `when` must be exhaustive\n\n定义密封类型，`when` 必须穷举",
		"type":        "`type` — Define a custom type or generic type\n\n定义自定义类型或泛型类型",
		"api":         "`api` — Define an API endpoint\n\n定义 API 接口",
		"fn":          "`fn` — Define a function\n\n定义函数",
		"val":         "`val` — Declare an immutable variable\n\n定义不可变变量",
		"when":        "`when` — Pattern matching expression\n\n模式匹配表达式",
		"if":          "`if` — Conditional statement\n\n条件判断语句",
		"else":        "`else` — Else branch of if statement\n\nif 语句的 else 分支",
		"for":         "`for` — Loop over a collection\n\n遍历集合",
		"in":          "`in` — Used in for loops and range checks\n\n用于 for 循环和范围检查",
		"return":      "`return` — Early return from function (last expression is implicit return)\n\n提前返回（最后一行表达式自动返回）",
		"find":        "`find` — Query database records\n\n查询数据库记录",
		"create":      "`create` — Create a database record\n\n创建数据库记录",
		"update":      "`update` — Update a database record\n\n更新数据库记录",
		"delete":      "`delete` — Delete a database record\n\n删除数据库记录",
		"transaction": "`transaction` — Database transaction, all-or-nothing\n\n数据库事务，全成功或全回滚",
		"emit":        "`emit` — Send async event (message queue + WebSocket)\n\n发送异步事件（消息队列 + WebSocket）",
		"throw":       "`throw` — Throw an error\n\n抛出错误",
		"extend":      "`extend` — Extend a type across modules (gateway aggregation)\n\n跨模块扩展类型（网关聚合）",
		"use":         "`use` — Import from a shared module\n\n从共享模块导入",
		"override":    "`override` — Override an auto-generated API implementation\n\n覆盖自动生成的 API 实现",
		"stream":      "`stream` — Server push via WebSocket\n\n通过 WebSocket 流式推送",
		"through":     "`through` — Many-to-many with explicit join table\n\n多对多显式中间表",
		"is":          "`is` — Type check in `when` branches\n\n`when` 分支中的类型匹配",
		"null":        "`null` — Null value\n\n空值",
		"true":        "`true` — Boolean true\n\n布尔真",
		"false":       "`false` — Boolean false\n\n布尔假",
	}
	return descriptions[word]
}

// ========== Definition ==========

func (s *Server) handleDefinition(req *Request) error {
	var params DefinitionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	doc := s.docs.Get(params.TextDocument.URI)
	if doc == nil || doc.Result == nil {
		return s.transport.SendResponse(req.ID, nil)
	}

	word := doc.WordAt(params.Position)
	if word == "" {
		return s.transport.SendResponse(req.ID, nil)
	}

	var defPos *token.Position

	// type definition
	if typ, ok := doc.Result.Types[word]; ok && typ.Pos.File != "" {
		defPos = &typ.Pos
	}

	// symbol definition
	if defPos == nil {
		if sym := doc.Result.Scope.Lookup(word); sym != nil && sym.Pos.File != "" {
			defPos = &sym.Pos
		}
	}

	if defPos == nil {
		return s.transport.SendResponse(req.ID, nil)
	}

	location := Location{
		URI: PathToURI(defPos.File),
		Range: Range{
			Start: Position{Line: defPos.Line - 1, Character: defPos.Col - 1},
			End:   Position{Line: defPos.Line - 1, Character: defPos.Col - 1 + len(word)},
		},
	}
	return s.transport.SendResponse(req.ID, location)
}
