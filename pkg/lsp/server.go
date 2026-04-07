package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/formatter"
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
	case "initialized", "textDocument/didSave":
		return nil
	case "shutdown":
		s.shutdown = true
		return s.transport.SendResponse(req.ID, nil)
	case "exit":
		return nil
	default:
		return s.handleTextDocument(req)
	}
}

func (s *Server) handleTextDocument(req *Request) error {
	switch req.Method {
	case "textDocument/didOpen":
		return s.handleDidOpen(req)
	case "textDocument/didChange":
		return s.handleDidChange(req)
	case "textDocument/didClose":
		return s.handleDidClose(req)
	case "textDocument/completion":
		return s.handleCompletion(req)
	case "textDocument/hover":
		return s.handleHover(req)
	case "textDocument/definition":
		return s.handleDefinition(req)
	case "textDocument/references":
		return s.handleReferences(req)
	case "textDocument/formatting":
		return s.handleFormatting(req)
	case "textDocument/inlayHint":
		return s.handleInlayHint(req)
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
			HoverProvider:              true,
			DefinitionProvider:         true,
			ReferencesProvider:         true,
			DocumentFormattingProvider: true,
			InlayHintProvider:          true,
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

	// inside @scope(...) — suggest scope names from the API's return type model
	if doc.IsInsideScopeDirective(pos) && doc.File != nil {
		items = append(items, s.getScopeCompletions(doc, pos)...)
		return items
	}

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

	// built-in function completions
	items = append(items, getBuiltinFunctionCompletions(prefix)...)

	// type and symbol completions from analysis
	if doc.Result != nil && doc.Result.Scope != nil {
		items = append(items, s.getSymbolCompletions(doc, prefix)...)
	}

	// built-in type completions
	items = append(items, getBuiltinTypeCompletions(prefix)...)

	// CRUD context completions (find/create/update/delete params + model fields)
	items = append(items, s.getCRUDCompletions(doc, pos)...)

	// local variable/parameter completions
	items = append(items, getLocalCompletions(doc, pos)...)

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

	// stdlib module methods: time., http., json., crypto., math., log., request.
	if methods := getStdlibModuleMethods(objName); len(methods) > 0 {
		return methods
	}

	// look up the type
	if typ, ok := doc.Result.Types[objName]; ok {
		return s.getTypeMemberCompletions(typ, objName)
	}

	// variable member access: look up variable type
	items = append(items, s.getVariableMemberCompletions(doc, objName)...)

	// collection methods + string properties + DateTime methods + query methods
	items = append(items, getCollectionMethodCompletions()...)
	items = append(items, getDateTimeMethods()...)
	items = append(items, getQueryMethodCompletions()...)

	return items
}

func (s *Server) getTypeMemberCompletions(typ *semantic.ResolvedType, objName string) []CompletionItem {
	var items []CompletionItem

	// type member access: Role.USER, Role.ADMIN
	if typ.Kind == semantic.TypeEnum {
		for _, v := range typ.EnumValues {
			items = append(items, CompletionItem{
				Label:    v,
				Kind:     20, // EnumMember
				Detail:   objName + "." + v,
				SortText: "a_" + v,
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
			Label:    name,
			Kind:     5, // Field
			Detail:   detail,
			SortText: "a_" + name,
		})
	}
	// inherited fields
	for _, parent := range typ.Parents {
		for name, field := range parent.Fields {
			detail := field.Type.Name + " (from " + parent.Name + ")"
			items = append(items, CompletionItem{
				Label:    name,
				Kind:     5,
				Detail:   detail,
				SortText: "b_" + name,
			})
		}
	}
	// CRUD chain methods for model types
	if typ.Kind == semantic.TypeModel {
		items = append(items, getQueryMethodCompletions()...)
	}
	return items
}

func (s *Server) getVariableMemberCompletions(doc *Document, objName string) []CompletionItem {
	var items []CompletionItem
	sym := doc.Result.Scope.Lookup(objName)
	if sym != nil && sym.Type != nil {
		for name, field := range sym.Type.Fields {
			detail := ""
			if field.Type != nil {
				detail = field.Type.Name
			}
			items = append(items, CompletionItem{
				Label:    name,
				Kind:     5,
				Detail:   detail,
				SortText: "a_" + name,
			})
		}
		return items
	}

	// Also check AST parameters for type resolution
	items = append(items, s.getParamMemberCompletions(doc, objName)...)
	return items
}

// getParamMemberCompletions resolves a parameter's type from the AST and returns field completions.
func (s *Server) getParamMemberCompletions(doc *Document, paramName string) []CompletionItem {
	if doc.File == nil || doc.Result == nil {
		return nil
	}
	typeName := findParamTypeName(doc.File, paramName)
	if typeName == "" {
		return nil
	}
	typ, ok := doc.Result.Types[typeName]
	if !ok {
		return nil
	}
	return s.getTypeMemberCompletions(typ, typeName)
}

func getCollectionMethodCompletions() []CompletionItem {
	collectionMethods := []struct {
		name   string
		detail string
	}{
		// collection methods
		{"filter", "filter { condition } — 过滤"},
		{"map", "map { transform } — 转换"},
		{"sumOf", "sumOf { expr } — 求和"},
		{"count", "count { condition } — 计数"},
		{"any", "any { condition } — 任一匹配"},
		{"all", "all { condition } — 全部匹配"},
		{"firstOrNull", "firstOrNull { condition } — 第一个匹配"},
		{"sortBy", "sortBy { field } — 排序"},
		{"groupBy", "groupBy { field } — 分组"},
		{"forEach", "forEach { action } — 遍历"},
		{"joinToString", "joinToString(separator) — 连接为字符串"},
		{"first", "first — 第一个元素"},
		{"last", "last — 最后一个元素"},
		{"distinct", "distinct — 去重"},
		{"flatten", "flatten — 展平嵌套集合"},
		{"take", "take(n) — 取前 N 个"},
		{"drop", "drop(n) — 跳过前 N 个"},
		{"reversed", "reversed — 反转"},
		{"size", "size — 集合大小"},
		{"isEmpty", "isEmpty — 是否为空"},
		{"let", "let { transform } — 作用域函数"},
		// string methods
		{"length", "length — 字符串长度"},
		{"contains", "contains(value) — 包含"},
		{"startsWith", "startsWith(prefix) — 以…开头"},
		{"endsWith", "endsWith(suffix) — 以…结尾"},
		{"lowercase", "lowercase — 转小写"},
		{"uppercase", "uppercase — 转大写"},
		{"trim", "trim — 去首尾空白"},
		{"trimStart", "trimStart — 去首空白"},
		{"trimEnd", "trimEnd — 去尾空白"},
		{"split", "split(separator) — 拆分"},
		{"replace", "replace(old, new) — 替换"},
		{"replaceAll", "replaceAll(old, new) — 全部替换"},
		{"substring", "substring(start, end) — 截取"},
		{"repeat", "repeat(n) — 重复 N 次"},
		{"padStart", "padStart(length, char) — 左填充"},
		{"padEnd", "padEnd(length, char) — 右填充"},
		{"matches", "matches(regex) — 正则匹配"},
		{"toInt", "toInt — 转为整数"},
		{"toFloat", "toFloat — 转为浮点数"},
		// debug chain methods
		{"d", "d — debug log / 调试日志"},
		{"i", "i — info log / 信息日志"},
		{"w", "w — warn log / 警告日志"},
		{"e", "e — error log / 错误日志"},
	}
	items := make([]CompletionItem, 0, len(collectionMethods))
	for _, m := range collectionMethods {
		items = append(items, CompletionItem{
			Label:    m.name,
			Kind:     2, // Method
			Detail:   m.detail,
			SortText: "z_" + m.name,
		})
	}
	return items
}

// getQueryMethodCompletions returns CRUD chain method completions for Model/QueryBuilder types.
func getQueryMethodCompletions() []CompletionItem {
	methods := []struct {
		name   string
		detail string
	}{
		{"where", "where(condition) — filter records / 按条件筛选记录"},
		{"all", "all() — fetch all matching records / 获取所有匹配记录"},
		{"first", "first() — fetch first matching record / 获取第一条匹配记录"},
		{"find", "find(id) — find record by ID, throws NotFound if missing / 按 ID 查找，找不到自动抛出 NotFound"},
		{"create", "create(fields) — create a record / 创建记录"},
		{"createMany", "createMany(records) — create multiple records / 批量创建记录"},
		{"update", "update(fields) — update a record / 更新记录"},
		{"delete", "delete() — delete a record / 删除记录"},
		{"deleteMany", "deleteMany(condition) — delete multiple records / 批量删除记录"},
		{"orderBy", "orderBy(field.asc/desc) — sort results / 排序"},
		{"limit", "limit(n) — limit number of results / 限制返回数量"},
		{"offset", "offset(n) — skip N results / 跳过 N 条结果"},
		{"groupBy", "groupBy { field } — group by field / 按字段分组"},
		{"select", "select { fields } — project/transform results / 投影"},
		{"sum", "sum { field } — sum field values / 求和"},
		{"avg", "avg { field } — average field values / 求平均"},
		{"count", "count() — count matching records / 计数"},
		{"min", "min { field } — minimum value / 最小值"},
		{"max", "max { field } — maximum value / 最大值"},
		{"exists", "exists() — check if records exist / 检查记录是否存在"},
		{"upsert", "upsert(fields) — create or update record / 创建或更新记录"},
		{"paginate", "paginate(page:, size:) — paginated query / 分页查询"},
		{"save", "save() — save dirty fields / 保存修改的字段"},
	}
	items := make([]CompletionItem, 0, len(methods))
	for _, m := range methods {
		items = append(items, CompletionItem{
			Label:    m.name,
			Kind:     2, // Method
			Detail:   m.detail,
			SortText: "c_" + m.name,
		})
	}
	return items
}

func getStdlibModuleMethods(module string) []CompletionItem {
	methods := map[string][]struct{ name, detail string }{
		"time": {
			{"now", "now(): DateTime — current time / 当前时间"},
			{"parse", "parse(str, layout): DateTime — parse time string / 解析时间字符串"},
			{"since", "since(t): Duration — duration since t / 距 t 的时间"},
			{"until", "until(t): Duration — duration until t / 到 t 的时间"},
		},
		"http": {
			{"get", "get(url): Response — HTTP GET request / HTTP GET 请求"},
			{"post", "post(url, body): Response — HTTP POST request / HTTP POST 请求"},
			{"put", "put(url, body): Response — HTTP PUT request / HTTP PUT 请求"},
			{"delete", "delete(url): Response — HTTP DELETE request / HTTP DELETE 请求"},
			{"patch", "patch(url, body): Response — HTTP PATCH request / HTTP PATCH 请求"},
		},
		"json": {
			{"parse", "parse(str): Json — parse JSON string / 解析 JSON 字符串"},
			{"stringify", "stringify(value): String — convert to JSON string / 转为 JSON 字符串"},
		},
		"crypto": {
			{"randomToken", "randomToken(length): String — generate random hex token / 生成随机令牌"},
			{"hash", "hash(value): String — hash value (bcrypt) / 哈希值"},
			{"verify", "verify(plain, hashed): Boolean — verify hash / 验证哈希"},
			{"encrypt", "encrypt(data, key): String — encrypt data / 加密数据"},
			{"decrypt", "decrypt(data, key): String — decrypt data / 解密数据"},
			{"uuid", "uuid(): String — generate UUID v4 / 生成 UUID"},
			{"hmac", "hmac(data, secret): String — HMAC-SHA256 / HMAC-SHA256 签名"},
			{"sha256", "sha256(data): String — SHA-256 digest / SHA-256 摘要"},
		},
		"math": {
			{"abs", "abs(x): Number — absolute value / 绝对值"},
			{"max", "max(a, b): Number — maximum / 最大值"},
			{"min", "min(a, b): Number — minimum / 最小值"},
			{"ceil", "ceil(x): Int — round up / 向上取整"},
			{"floor", "floor(x): Int — round down / 向下取整"},
			{"round", "round(x): Int — round to nearest / 四舍五入"},
			{"random", "random(): Float — random 0.0~1.0 / 随机数"},
			{"pow", "pow(base, exp): Float — power / 幂运算"},
			{"sqrt", "sqrt(x): Float — square root / 平方根"},
			{"clamp", "clamp(value, min, max): Number — clamp to range / 限制范围"},
		},
		"convert": {
			{"toInt", "toInt(value): Int — convert to Int / 转为整数"},
			{"toFloat", "toFloat(value): Float — convert to Float / 转为浮点数"},
			{"toString", "toString(value): String — convert to String / 转为字符串"},
			{"toBool", "toBool(value): Boolean — convert to Boolean / 转为布尔值"},
			{"tryInt", "tryInt(value): Int? — safe convert to Int / 安全转为整数"},
			{"tryFloat", "tryFloat(value): Float? — safe convert to Float / 安全转为浮点数"},
		},
		"request": {
			{"ip", "ip: String — client IP address / 客户端 IP"},
			{"method", "method: String — HTTP method / HTTP 方法"},
			{"path", "path: String — request path / 请求路径"},
			{"header", "header(name): String — get header value / 获取请求头"},
			{"query", "query(name): String — get query param / 获取查询参数"},
			{"body", "body: String — request body / 请求体"},
			{"fields", "fields: FieldSet — client requested fields / 客户端请求的字段集"},
		},
		"my": {
			{"id", "id: Int — user ID from JWT (zero-cost) / JWT 中的用户 ID（零开销）"},
			{"load", "load(fields...): Identity — lazy load user fields / 懒加载用户字段"},
			{"role", "role — user role from JWT / JWT 中的用户角色"},
		},
	}

	defs, ok := methods[module]
	if !ok {
		return nil
	}
	items := make([]CompletionItem, 0, len(defs))
	for _, m := range defs {
		items = append(items, CompletionItem{
			Label:  m.name,
			Kind:   3, // Function
			Detail: m.detail,
		})
	}
	return items
}

func getDateTimeMethods() []CompletionItem {
	methods := []struct{ name, detail string }{
		{"format", "format(layout): String — format as string / 格式化为字符串"},
		{"add", "add(duration): DateTime — add duration / 加时间段"},
		{"sub", "sub(duration): DateTime — subtract duration / 减时间段"},
		{"before", "before(other): Boolean — is before other / 是否早于"},
		{"after", "after(other): Boolean — is after other / 是否晚于"},
		{"year", "year: Int — year component / 年"},
		{"month", "month: Int — month component / 月"},
		{"day", "day: Int — day component / 日"},
		{"hour", "hour: Int — hour component / 时"},
		{"minute", "minute: Int — minute component / 分"},
		{"second", "second: Int — second component / 秒"},
		{"unix", "unix: Int — unix timestamp / Unix 时间戳"},
	}
	items := make([]CompletionItem, 0, len(methods))
	for _, m := range methods {
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
		// database type annotations
		{"length", "string length limit"}, {"serial", "auto-increment"},
		{"bigint", "big integer"}, {"smallint", "small integer"},
		{"decimal", "high precision float"}, {"uuid", "UUID type"},
		{"inet", "IP address type"}, {"point", "geographic coordinate"},
		{"date", "date only"}, {"time", "time only"},
		{"vector", "vector type (pgvector)"}, {"brin", "BRIN index"},
		// model annotations
		{"soft", "soft delete"}, {"noTime", "disable auto timestamps"},
		{"encrypt", "salted encryption at rest"},
		// api annotations
		{"global", "global middleware"}, {"broadcast", "broadcast event"},
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
		{"val", "immutable variable", "val ${1:name} = $0"},
		{"var", "mutable variable", "var ${1:name} = $0"},
		{"when", "when expression", "when {\n\t$0\n}"},
		{"if", "if guard (no else, use when)", "if ${1:condition} {\n\t$0\n}"},
		{"for", "for loop", "for ${1:item} in ${2:collection} {\n\t$0\n}"},
		{"return", "return", "return $0"},
		{"throw", "throw error", "throw error.$0"},
		{"break", "exit loop", ""},
		{"continue", "skip to next iteration", ""},
		{"yield", "exit loop with value", "yield $0"},
		{"async", "launch coroutine", "async {\n\t$0\n}"},
		{"await", "concurrent wait", "await {\n\t$0\n}"},
		{"use", "use stdlib or module", "use $0"},
		{"override", "override api", "override api $0"},
		{"true", "boolean true", ""},
		{"false", "boolean false", ""},
		{"null", "null value", ""},
		{"else", "else branch", ""},
		{"in", "membership / range", ""},
		{"is", "type check", ""},
		{"event", "event declaration", "event ${1:Name}(${2:params}) "},
		{"on", "event listener", "on ${1:EventName} {\n\t$0\n}"},
		{"my", "current user", ""},
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

func getBuiltinFunctionCompletions(prefix string) []CompletionItem {
	builtins := []struct {
		label, detail, snippet string
	}{
		{"find", "find records", "find(${1:Model}, ${2:where: condition})"},
		{"create", "create record", "create(${1:Model}, ${2:field: value})"},
		{"update", "update record", "update(${1:record}, ${2:field: value})"},
		{"delete", "delete record", "delete(${1:record})"},
		{"emit", "emit event", "emit(\"$1\", $0)"},
		{"transaction", "transaction block", "transaction {\n\t$0\n}"},
		{"cache", "cache operation", "cache($0)"},
		{"storage", "storage operation", "storage($0)"},
		{"mail", "send mail", "mail($0)"},
		{"task", "async task", "task($0)"},
	}

	var items []CompletionItem
	for _, fn := range builtins {
		if prefix == "" || strings.HasPrefix(fn.label, prefix) {
			item := CompletionItem{
				Label:  fn.label,
				Kind:   3, // Function
				Detail: "built-in: " + fn.detail,
			}
			if fn.snippet != "" {
				item.InsertText = fn.snippet
				item.Kind = 15 // Snippet
			}
			items = append(items, item)
		}
	}
	return items
}

func getBuiltinTypeCompletions(prefix string) []CompletionItem {
	types := []string{"String", "Int", "Float", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "Void", "Result", "Channel"}
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

// ========== Local Completions ==========

// getLocalCompletions collects parameters and val declarations visible at pos.
func getLocalCompletions(doc *Document, pos Position) []CompletionItem {
	if doc.File == nil {
		return nil
	}

	var items []CompletionItem
	items = append(items, getAPILocalCompletions(doc.File, pos)...)
	items = append(items, getFnLocalCompletions(doc.File, pos)...)
	return items
}

// getAPILocalCompletions collects params and locals from the API enclosing pos.
func getAPILocalCompletions(file *ast.File, pos Position) []CompletionItem {
	var items []CompletionItem
	for _, api := range file.APIs {
		apiStartLine := api.Pos.Line - 1 // convert 1-based to 0-based
		if pos.Line < apiStartLine {
			continue
		}
		for _, p := range api.Params {
			items = append(items, CompletionItem{
				Label:  p.Name,
				Kind:   6, // Variable
				Detail: "param: " + typeRefToString(p.Type),
			})
		}
		if api.Body != nil {
			items = append(items, collectValsFromBlock(api.Body, pos)...)
		}
		// currentUser is available inside any api body
		items = append(items, CompletionItem{
			Label:  "currentUser",
			Kind:   6,
			Detail: "current authenticated user",
		})
	}
	return items
}

// getFnLocalCompletions collects params and locals from the fn enclosing pos.
func getFnLocalCompletions(file *ast.File, pos Position) []CompletionItem {
	var items []CompletionItem
	for _, fn := range file.Functions {
		fnStartLine := fn.Pos.Line - 1
		if pos.Line < fnStartLine {
			continue
		}
		if fn.Body == nil {
			continue
		}
		for _, p := range fn.Params {
			items = append(items, CompletionItem{
				Label:  p.Name,
				Kind:   6,
				Detail: "param: " + typeRefToString(p.Type),
			})
		}
		items = append(items, collectValsFromBlock(fn.Body, pos)...)
	}
	return items
}

// collectValsFromBlock collects val declarations and for-loop variables before pos.
func collectValsFromBlock(block *ast.Block, pos Position) []CompletionItem {
	var items []CompletionItem
	for _, stmt := range block.Stmts {
		if stmt.GetPos().Line-1 > pos.Line {
			break
		}
		if vs, ok := stmt.(*ast.ValStmt); ok {
			detail := "val"
			if vs.Mutable {
				detail = "var"
			}
			items = append(items, CompletionItem{
				Label:  vs.Name,
				Kind:   6,
				Detail: detail,
			})
		}
		if fs, ok := stmt.(*ast.ForStmt); ok {
			if fs.VarName != "" {
				items = append(items, CompletionItem{
					Label:  fs.VarName,
					Kind:   6,
					Detail: "loop variable",
				})
			}
		}
	}
	return items
}

// findParamTypeName searches all api/fn declarations for a parameter with the given name.
func findParamTypeName(file *ast.File, paramName string) string {
	for _, api := range file.APIs {
		for _, p := range api.Params {
			if p.Name == paramName && p.Type != nil {
				return p.Type.Name
			}
		}
	}
	for _, fn := range file.Functions {
		for _, p := range fn.Params {
			if p.Name == paramName && p.Type != nil {
				return p.Type.Name
			}
		}
	}
	return ""
}

// typeRefToString converts a TypeRef to a human-readable string.
func typeRefToString(t *ast.TypeRef) string {
	if t == nil {
		return ""
	}
	name := t.Name
	if t.IsList {
		name = "[" + name + "]"
	}
	if t.Nullable {
		name += "?"
	}
	return name
}

// getLocalHover returns a hover string for a local variable, parameter, or field.
func getLocalHover(doc *Document, word string, pos Position) string {
	if doc.File == nil {
		return ""
	}
	if !doc.IsAfterItDot(pos) {
		if hover := getParamHover(doc.File, word, pos); hover != "" {
			return hover
		}
	}
	if hover := getValVarHover(doc.File, word, pos); hover != "" {
		return hover
	}
	// inside CRUD call, resolve field against the target model
	if hover := getCRUDFieldHover(doc, word, pos); hover != "" {
		return hover
	}
	return getFieldHover(doc.File, word)
}

// getCRUDFieldHover resolves a field name against the CRUD target model.
// Inside find(RoomMember, where: ...), bare field names resolve to RoomMember's fields.
func getCRUDFieldHover(doc *Document, word string, pos Position) string {
	funcName, modelName := doc.FindEnclosingCall(pos)
	if !isCRUDFunc(funcName) || modelName == "" {
		return ""
	}
	if doc.File == nil {
		return ""
	}
	for _, m := range doc.File.Models {
		if m.Name == modelName {
			for _, f := range m.Fields {
				if f.Name == word {
					return formatFieldHover(m.Name, f)
				}
			}
		}
	}
	return ""
}

func getParamHover(file *ast.File, word string, pos Position) string {
	for _, api := range file.APIs {
		if pos.Line < api.Pos.Line-1 {
			continue
		}
		for _, p := range api.Params {
			if p.Name == word {
				return "```luxo\nparam " + p.Name + ": " + typeRefToString(p.Type) + "\n```"
			}
		}
	}
	for _, fn := range file.Functions {
		if pos.Line < fn.Pos.Line-1 {
			continue
		}
		for _, p := range fn.Params {
			if p.Name == word {
				return "```luxo\nparam " + p.Name + ": " + typeRefToString(p.Type) + "\n```"
			}
		}
	}
	return ""
}

func getValVarHover(file *ast.File, word string, pos Position) string {
	for _, api := range file.APIs {
		if api.Body != nil {
			if hover := findValHover(api.Body, word, pos); hover != "" {
				return hover
			}
		}
	}
	for _, fn := range file.Functions {
		if fn.Body != nil {
			if hover := findValHover(fn.Body, word, pos); hover != "" {
				return hover
			}
		}
	}
	return ""
}

func getFieldHover(file *ast.File, word string) string {
	for _, m := range file.Models {
		for _, f := range m.Fields {
			if f.Name == word {
				return formatFieldHover(m.Name, f)
			}
		}
	}
	for _, t := range file.Types {
		for _, f := range t.Fields {
			if f.Name == word {
				return formatFieldHover(t.Name, f)
			}
		}
	}
	return ""
}

func findValHover(block *ast.Block, word string, pos Position) string {
	for _, stmt := range block.Stmts {
		if stmt.GetPos().Line-1 > pos.Line {
			break
		}
		if vs, ok := stmt.(*ast.ValStmt); ok {
			if vs.Name == word {
				kind := "val"
				if vs.Mutable {
					kind = "var"
				}
				typeStr := ""
				if vs.Type != nil {
					typeStr = ": " + typeRefToString(vs.Type)
				}
				return "```luxo\n" + kind + " " + vs.Name + typeStr + "\n```"
			}
		}
	}
	return ""
}

// formatFieldHover formats a hover string for a model field with doc comment.
func formatFieldHover(modelName string, f *ast.FieldDecl) string {
	var b strings.Builder
	b.WriteString("```luxo\n")
	b.WriteString(modelName + "." + f.Name + ": " + typeRefToString(f.Type))
	if len(f.Directives) > 0 {
		for _, d := range f.Directives {
			b.WriteString(" @" + d.Name)
		}
	}
	b.WriteString("\n```")
	if f.Doc != "" {
		b.WriteString("\n\n---\n\n" + f.Doc)
	}
	return b.String()
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

	hover := s.resolveHover(doc, word, params.Position)
	if hover == "" {
		return s.transport.SendResponse(req.ID, nil)
	}
	return s.transport.SendResponse(req.ID, Hover{
		Contents: MarkupContent{Kind: "markdown", Value: hover},
	})
}

// resolveHover returns a hover string for the word at the given position, or "" if none.
func (s *Server) resolveHover(doc *Document, word string, pos Position) string {
	if hover := s.resolveContextualHover(doc, word, pos); hover != "" {
		return hover
	}
	if hover := resolveSemanticHover(doc, word, pos); hover != "" {
		return hover
	}
	return resolveBuiltinHover(doc, word, pos)
}

// resolveContextualHover checks position-dependent hovers (directives, scopes, built-in types).
func (s *Server) resolveContextualHover(doc *Document, word string, pos Position) string {
	if doc.PrevChar(pos) == '@' || doc.IsAfterAt(pos) {
		if desc := directiveDescription(word); desc != "" {
			return desc
		}
	}
	if doc.IsInsideScopeDirective(pos) && doc.File != nil {
		if hover := scopeNameHover(doc, word); hover != "" {
			return hover
		}
	}
	if desc := builtinTypeDescription(word); desc != "" {
		return desc
	}
	return ""
}

// resolveSemanticHover checks type, symbol, and local variable hovers.
func resolveSemanticHover(doc *Document, word string, pos Position) string {
	if typ, ok := doc.Result.Types[word]; ok {
		return formatTypeHover(word, typ)
	}
	if sym := doc.Result.Scope.Lookup(word); sym != nil {
		return formatSymbolHover(sym)
	}
	if doc.File != nil {
		if hover := getLocalHover(doc, word, pos); hover != "" {
			return hover
		}
	}
	return ""
}

// resolveBuiltinHover checks member methods, built-in functions, and keyword hovers.
func resolveBuiltinHover(doc *Document, word string, pos Position) string {
	if doc.PrevChar(pos) == '.' || doc.IsAfterDot(pos) {
		if desc := memberMethodDescription(word); desc != "" {
			return desc
		}
	}
	if desc := builtinFunctionDescription(word); desc != "" {
		return desc
	}
	return keywordDescription(word)
}

func formatTypeHover(name string, typ *semantic.ResolvedType) string {
	var b strings.Builder
	b.WriteString("```luxo\n")

	switch typ.Kind {
	case semantic.TypeModel:
		formatModelHover(&b, name, typ)
	case semantic.TypeEnum:
		formatEnumHover(&b, name, typ)
	case semantic.TypeSealed:
		formatSealedHover(&b, name, typ)
	default:
		b.WriteString("type " + name)
	}

	b.WriteString("\n```")
	return b.String()
}

func formatModelHover(b *strings.Builder, name string, typ *semantic.ResolvedType) {
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
		if field.Doc != "" {
			b.WriteString("  // " + field.Doc)
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
}

func formatEnumHover(b *strings.Builder, name string, typ *semantic.ResolvedType) {
	b.WriteString("enum " + name + " {\n")
	for _, v := range typ.EnumValues {
		b.WriteString("  " + v + "\n")
	}
	b.WriteString("}")
}

func formatSealedHover(b *strings.Builder, name string, typ *semantic.ResolvedType) {
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
		"model":     "`model` — Define a data model, maps to database table\n\n定义数据模型，映射到数据库表",
		"interface": "`interface` — Define an interface with optional default implementations\n\n定义接口，可带默认实现",
		"enum":      "`enum` — Define an enumeration type\n\n定义枚举类型",
		"sealed":    "`sealed` — Define a sealed type, `when` must be exhaustive\n\n定义密封类型，`when` 必须穷举",
		"type":      "`type` — Define a custom type or generic type\n\n定义自定义类型或泛型类型",
		"api":       "`api` — Define an API endpoint\n\n定义 API 接口",
		"fn":        "`fn` — Define a function\n\n定义函数",
		"val":       "`val` — Declare an immutable variable\n\n定义不可变变量",
		"var":       "`var` — Declare a mutable variable\n\n定义可变变量",
		"when":      "`when` — Pattern matching expression\n\n模式匹配表达式",
		"if":        "`if` — Single-branch guard (no else, use `when` for multi-branch)\n\n单分支守卫（无 else，多分支用 `when`）",
		"else":      "`else` — Default branch in `when` expression\n\n`when` 表达式的默认分支",
		"for":       "`for` — Loop over a collection\n\n遍历集合",
		"in":        "`in` — Used in for loops and range checks\n\n用于 for 循环和范围检查",
		"return":    "`return` — Early return from function (last expression is implicit return)\n\n提前返回（最后一行表达式自动返回）",
		"throw":     "`throw` — Throw an error\n\n抛出错误",
		"break":     "`break` — Exit the current loop\n\n退出当前循环",
		"continue":  "`continue` — Skip to the next iteration of the current loop\n\n跳过当前循环的剩余部分，进入下一次迭代",
		"yield":     "`yield` — Exit loop and return a value (for as expression)\n\n退出循环并返回值（for 作为表达式）",
		"async":     "`async` — Launch a coroutine (fire and forget)\n\n启动协程（不等待结果）",
		"await":     "`await` — Run multiple operations concurrently and wait for all\n\n并发执行多个操作并等待全部完成",
		"extend":    "`extend` — Extend a type across modules (gateway aggregation)\n\n跨模块扩展类型（网关聚合）",
		"use":       "`use` — Import a stdlib module (`use http`) or shared module (`use common.{ Base }`)\n\n导入标准库模块或共享模块",
		"override":  "`override` — Override an auto-generated API implementation\n\n覆盖自动生成的 API 实现",
		"is":        "`is` — Type check in `when` branches\n\n`when` 分支中的类型匹配",
		"event":     "`event` — Define a typed event\n\n定义类型化事件",
		"on":        "`on` — Subscribe to an event\n\n订阅事件",
		"my":        "`my` — Current authenticated user (my.id is always available, my.load() for full data)\n\n当前认证用户（my.id 零开销，my.load() 懒加载）",
		"null":      "`null` — Null value\n\n空值",
		"true":      "`true` — Boolean true\n\n布尔真",
		"false":     "`false` — Boolean false\n\n布尔假",
	}
	return descriptions[word]
}

func memberMethodDescription(word string) string {
	descriptions := map[string]string{
		// collection methods
		"size":         "`size` — Number of elements in collection\n\n集合元素数量",
		"filter":       "`filter { condition }` — Filter elements by condition\n\n按条件过滤元素",
		"map":          "`map { transform }` — Transform each element\n\n转换每个元素",
		"sumOf":        "`sumOf { expr }` — Sum of expression values\n\n表达式值的总和",
		"any":          "`any { condition }` — True if any element matches\n\n任意元素匹配则为 true",
		"count":        "`count { condition }` — Count matching elements\n\n计算匹配元素数量",
		"firstOrNull":  "`firstOrNull { condition }` — First matching element or null\n\n第一个匹配元素，无则 null",
		"sortBy":       "`sortBy { field }` — Sort by field\n\n按字段排序",
		"groupBy":      "`groupBy { field }` — Group by field\n\n按字段分组",
		"forEach":      "`forEach { action }` — Execute action for each element\n\n对每个元素执行操作",
		"joinToString": "`joinToString(separator)` — Join elements as string\n\n将元素连接为字符串",
		"first":        "`first` — First element\n\n第一个元素",
		"last":         "`last` — Last element\n\n最后一个元素",
		"distinct":     "`distinct` — Remove duplicates\n\n去重",
		// string properties
		"length":     "`length` — String length\n\n字符串长度",
		"isEmpty":    "`isEmpty` — True if string is empty\n\n字符串是否为空",
		"trim":       "`trim` — Remove leading/trailing whitespace\n\n去除首尾空白",
		"lowercase":  "`lowercase` — Convert to lowercase\n\n转为小写",
		"uppercase":  "`uppercase` — Convert to uppercase\n\n转为大写",
		"contains":   "`contains(substring)` — Check if contains substring\n\n是否包含子串",
		"startsWith": "`startsWith(prefix)` — Check if starts with prefix\n\n是否以前缀开头",
		"endsWith":   "`endsWith(suffix)` — Check if ends with suffix\n\n是否以后缀结尾",
		"split":      "`split(separator)` — Split string by separator\n\n按分隔符拆分",
		"replace":    "`replace(old, new)` — Replace occurrences\n\n替换子串",
		"substring":  "`substring(start, end)` — Extract substring\n\n截取子串",
		"let":        "`let { transform }` — Scope function, transform value\n\n作用域函数，转换值",
		// missing string methods
		"trimStart":  "`trimStart` — Remove leading whitespace\n\n去除首部空白",
		"trimEnd":    "`trimEnd` — Remove trailing whitespace\n\n去除尾部空白",
		"replaceAll": "`replaceAll(old, new)` — Replace all occurrences\n\n替换所有匹配",
		"matches":    "`matches(regex)` — Test regex match\n\n正则匹配",
		"repeat":     "`repeat(n)` — Repeat string N times\n\n重复 N 次",
		"padStart":   "`padStart(length, char)` — Pad start to length\n\n左填充到指定长度",
		"padEnd":     "`padEnd(length, char)` — Pad end to length\n\n右填充到指定长度",
		"toInt":      "`toInt` — Convert to Int\n\n转为整数",
		"toFloat":    "`toFloat` — Convert to Float\n\n转为浮点数",
		"reversed":   "`reversed` — Reverse string or collection\n\n反转",
		// missing collection methods
		"all":     "`all { condition }` — True if all elements match\n\n所有元素匹配则为 true",
		"flatten": "`flatten` — Flatten nested collection\n\n展平嵌套集合",
		"take":    "`take(n)` — Take first N elements\n\n取前 N 个元素",
		"drop":    "`drop(n)` — Drop first N elements\n\n跳过前 N 个元素",
		// DateTime methods
		"format": "`format(layout)` — Format DateTime as string\n\n格式化时间为字符串",
		"add":    "`add(duration)` — Add duration to DateTime\n\n加时间段",
		"sub":    "`sub(duration)` — Subtract duration from DateTime\n\n减时间段",
		"before": "`before(other)` — Is before other DateTime\n\n是否早于",
		"after":  "`after(other)` — Is after other DateTime\n\n是否晚于",
		"year":   "`year` — Year component\n\n年",
		"month":  "`month` — Month component\n\n月",
		"day":    "`day` — Day component\n\n日",
		"hour":   "`hour` — Hour component\n\n时",
		"minute": "`minute` — Minute component\n\n分",
		"second": "`second` — Second component\n\n秒",
		"unix":   "`unix` — Unix timestamp\n\nUnix 时间戳",
		// stdlib module methods (also accessible as hover on now/parse/etc)
		"now":         "`time.now()` — Current time\n\n当前时间",
		"parse":       "`parse(...)` — Parse from string\n\n解析字符串",
		"stringify":   "`json.stringify(value)` — Convert to JSON string\n\n转为 JSON 字符串",
		"get":         "`http.get(url)` — HTTP GET request\n\nHTTP GET 请求",
		"post":        "`http.post(url, body)` — HTTP POST request\n\nHTTP POST 请求",
		"randomToken": "`crypto.randomToken(length)` — Generate random token\n\n生成随机令牌",
		"hash":        "`crypto.hash(value)` — Hash value\n\n哈希值",
		"uuid":        "`crypto.uuid()` — Generate UUID\n\n生成 UUID",
		"ip":          "`request.ip` — Client IP address\n\n客户端 IP",
		"method":      "`request.method` — HTTP method\n\nHTTP 方法",
		"path":        "`request.path` — Request path\n\n请求路径",
		"header":      "`request.header(name)` — Get header value\n\n获取请求头",
		"query":       "`request.query(name)` — Get query parameter\n\n获取查询参数",
		"body":        "`request.body` — Request body\n\n请求体",
		// @withAuth injected methods
		"createToken":  "`.createToken(expires?: Duration): String` — Generate JWT token\n\n生成 JWT 令牌",
		"verify":       "`.verify(plain: String): Boolean` — Verify password against @hash field\n\n校验 @hash 字段的密码",
		"refreshToken": "`.refreshToken(token: String): String` — Refresh JWT token\n\n刷新 JWT 令牌",
		// Identity methods
		"load": "`.load(fields...): Identity` — Lazy load user fields from database\n\n从数据库懒加载用户字段",
		// CRUD chain methods
		"where":   "`where(condition)` — Filter records by condition / 按条件筛选记录\n\n`Model.where(field == value).all()`",
		"orderBy": "`orderBy(field.asc/desc)` — Sort results / 排序\n\n`Model.where(...).orderBy(field.desc)`",
		"limit":   "`limit(n)` — Limit number of results / 限制返回数量",
		"offset":  "`offset(n)` — Skip N results / 跳过 N 条结果",
		"sum":     "`sum { field }` — Sum field values (DB aggregate) / 求和\n\n`orders.sum { it.total }`",
		"avg":     "`avg { field }` — Average field values (DB aggregate) / 求平均\n\n`orders.avg { it.total }`",
		"min":     "`min { field }` — Minimum value (DB aggregate) / 最小值\n\n`orders.min { it.price }`",
		"max":     "`max { field }` — Maximum value (DB aggregate) / 最大值\n\n`orders.max { it.price }`",
		"select":  "`select { fields }` — Project/transform query results / 投影\n\n`Model.groupBy { it.status }.select { ... }`",
		// request.fields methods
		"fields": "`request.fields` — Client-requested field set / 客户端请求的字段集合\n\n`request.fields.has(\"posts\")`",
		"has":    "`has(name)` — Check if field was requested (O(1) hash lookup) / 检查字段是否被请求",
		// debug chain methods
		"d": "`.d` — Debug log, returns self for chaining\n\n调试日志，返回自身用于链式调用",
		"i": "`.i` — Info log, returns self for chaining\n\n信息日志，返回自身用于链式调用",
		"w": "`.w` — Warn log, returns self for chaining\n\n警告日志，返回自身用于链式调用",
		"e": "`.e` — Error log, returns self for chaining\n\n错误日志，返回自身用于链式调用",
		// dirty tracking save
		"save": "Save dirty fields to database / 保存修改的字段到数据库\n\n`post.status = ...; post.save()`",
	}
	if desc, ok := descriptions[word]; ok {
		return desc
	}
	return ""
}

func builtinFunctionDescription(word string) string {
	descriptions := map[string]string{
		"find":        "`Model.find(id)` — Find record by primary key, throws NotFound if not found. Use first() for nullable.\n\n按主键查找，找不到自动抛出 NotFound。如需 nullable 返回请用 first()",
		"create":      "`Model.create(...)` — Create a database record\n\n创建数据库记录",
		"update":      "`record.update(...)` — Update a database record\n\n更新数据库记录",
		"delete":      "`record.delete()` — Delete a database record\n\n删除数据库记录",
		"transaction": "`transaction { ... }` — Database transaction, all-or-nothing\n\n数据库事务，全成功或全回滚",
		"emit":        "`emit EventName(args)` — Send typed event (async). Inside transaction: delayed until commit, discarded on rollback\n\n发送类型化事件（异步）。事务内：延迟到提交后发送，回滚时丢弃",
		"cache":       "`cache(...)` — Cache operation\n\n缓存操作",
		"storage":     "`storage(...)` — Storage operation\n\n存储操作",
		"mail":        "`mail(...)` — Send mail\n\n发送邮件",
		"task":        "`task(...)` — Async task\n\n异步任务",
		"Channel":     "`Channel<T>(size)` — Create a buffered channel for coroutine communication\n\n创建缓冲 channel 用于协程通信",
		"http":        "`http.get(url)` / `http.post(url, body)` — HTTP client\n\nHTTP 客户端",
		"json":        "`json.parse(str)` / `json.stringify(obj)` — JSON serialization\n\nJSON 序列化",
		"time":        "`time.now()` / `time.today()` — Date and time\n\n日期时间",
		"math":        "`math.abs()` / `math.max()` / `math.clamp()` — Math functions\n\n数学函数",
		"crypto":      "`crypto.sha256()` / `crypto.randomToken()` / `crypto.uuid()` — Cryptographic functions\n\n加密函数",
		"convert":     "`convert.toInt()` / `convert.toFloat()` / `convert.tryInt()` — Type conversion\n\n类型转换",
		"request":     "`request.ip` / `request.header(name)` / `request.fields` — Current HTTP request context\n\n当前 HTTP 请求上下文",
		"aggregate":   "`aggregate(Model, sum:, avg:, ...)` — Aggregate query\n\n聚合查询",
		"groupBy":     "`groupBy(Model, by:, sum:, ...)` — Group by aggregation\n\n分组聚合",
		"raw":         "`raw<T>(sql, params:)` — SQL escape hatch (⚠ bypasses type safety)\n\nSQL 逃生舱（⚠ 绕过类型安全）",
		"paginate":    "`paginate(Model, page:, ...)` — Offset or cursor pagination\n\n分页查询（offset 或 cursor）",
	}
	return descriptions[word]
}

func directiveDescription(name string) string {
	descriptions := map[string]string{
		// database type annotations
		"length":   "`@length(n)` — PG: varchar(n), Go: string\n\n限制字符串长度",
		"serial":   "`@serial` — PG: serial (auto-increment), Go: int64\n\n自增主键",
		"bigint":   "`@bigint` — PG: bigint, Go: int64\n\n大整数",
		"smallint": "`@smallint` — PG: smallint, Go: int16\n\n小整数",
		"decimal":  "`@decimal(p, s)` — PG: numeric(p,s), Go: decimal.Decimal\n\n高精度浮点",
		"uuid":     "`@uuid` — PG: uuid, Go: string\n\nUUID 类型",
		"inet":     "`@inet` — PG: inet, Go: string\n\nIP 地址类型",
		"point":    "`@point` — PG: point, Go: [2]float64\n\n地理坐标",
		"date":     "`@date` — PG: date, Go: time.Time\n\n仅日期",
		"time":     "`@time` — PG: time, Go: time.Time\n\n仅时间",
		"vector":   "`@vector(n)` — PG: vector(n) (pgvector), Go: []float32\n\n向量类型",
		"brin":     "`@brin` — PG: BRIN index (time-series optimization)\n\n时序索引",
		// model annotations
		"crud":   "`@crud` — Auto-generate admin CRUD APIs (get/list/create/update/delete)\n\n自动生成后台 CRUD 接口",
		"soft":   "`@soft` — Soft delete (deletedAt column, auto WHERE deletedAt IS NULL)\n\n软删除",
		"noTime": "`@noTime` — Disable auto createdAt/updatedAt timestamps\n\n禁用自动时间戳",
		// field annotations
		"unique":     "`@unique` — PG: UNIQUE constraint\n\n唯一约束",
		"index":      "`@index` — PG: B-tree index\n\n数据库索引",
		"hidden":     "`@hidden` — Never returned to client, write-only\n\n不返回客户端，只写",
		"hash":       "`@hash` — Auto bcrypt hash on save\n\n保存时自动 bcrypt 加密",
		"immutable":  "`@immutable` — Cannot change after create\n\n创建后不可修改",
		"internal":   "`@internal` — Only set by server, not by client\n\n仅服务端设置",
		"filterable": "`@filterable` — Enable filtering in list queries\n\n启用列表过滤",
		"sortable":   "`@sortable` — Enable sorting in list queries\n\n启用列表排序",
		"search":     "`@search` — PG: tsvector + GIN index, full-text search\n\n全文搜索",
		"mask":       "`@mask(pattern)` — Return masked value (e.g. 138****1234)\n\n返回脱敏值",
		"encrypt":    "`@encrypt` — Salted encryption at rest, auto decrypt on read\n\n加盐加密存储",
		"deprecated": "`@deprecated(msg)` — Mark field as deprecated\n\n标记字段已弃用",
		"visible":    "`@visible(expr)` — Conditional visibility\n\n条件可见",
		"transform":  "`@transform { expr }` — Transform value on return\n\n返回时转换",
		"beforeSave": "`@beforeSave { expr }` — Transform before saving\n\n保存前转换",
		// api annotations
		"auth":      "`@auth` / `@auth(User, Admin)` / `@auth(Admin, permission: { it.role == ... })` — Require authentication with optional identity and permission\n\n认证鉴权，可指定认证主体和权限",
		"withAuth":  "`@withAuth(jwt: { secret: env(\"...\"), expires: 7d }, stores: [id])` — Mark model as auth subject, auto-generates .createToken()/.verify()\n\n标记模型为认证主体，自动生成认证方法",
		"native":    "`@native` — Implementation in Go\n\n使用 Go 实现",
		"cache":     "`@cache(ttl: duration)` — Cache response\n\n缓存响应",
		"rateLimit": "`@rateLimit(max, window)` — Rate limiting\n\n限流",
		"global":    "`@global` — Global middleware\n\n全局中间件",
		"retry":     "`@retry(max, delay)` — Retry on failure\n\n失败重试",
		"broadcast": "`@broadcast` — All instances receive event\n\n所有实例都接收事件",
		"scope":     "`@scope(scopeNames...)` — Apply query scope(s) to API\n\n应用查询预设",
		"stream":    "`@stream` — Enable streaming response via WebSocket\n\n启用 WebSocket 流式推送",
		"paginate":  "`@paginate` / `@paginate(defaultPageSize: 50)` — Auto-inject page/pageSize params, wrap return type [T] → Page<T>\n\n自动注入分页参数，返回类型从 [T] 包装为 Page<T>",
	}
	if desc, ok := descriptions[name]; ok {
		return desc
	}
	return ""
}

func builtinTypeDescription(name string) string {
	descriptions := map[string]string{
		"Int":      "```\nInt\n```\n\nGo: `int64` | PG: `integer`\n\nAnnotations: `@serial` (auto-increment), `@bigint`, `@smallint`",
		"Float":    "```\nFloat\n```\n\nGo: `float64` | PG: `double precision`\n\nAnnotations: `@decimal(p, s)` → PG numeric",
		"String":   "```\nString\n```\n\nGo: `string` | PG: `text`\n\nAnnotations: `@length(n)` → varchar(n), `@uuid`, `@inet`",
		"Boolean":  "```\nBoolean\n```\n\nGo: `bool` | PG: `boolean`",
		"DateTime": "```\nDateTime\n```\n\nGo: `time.Time` | PG: `timestamptz`\n\nAnnotations: `@date` → date only, `@time` → time only",
		"Duration": "```\nDuration\n```\n\nGo: `time.Duration` | PG: `interval`\n\nLiterals: `7d`, `5m`, `1h`, `30s`, `500ms`",
		"UUID":     "```\nUUID\n```\n\nGo: `uuid.UUID` | PG: `uuid` | MySQL: `BINARY(16)` | Mongo: native\n\nUse `@auto` for auto-generated UUID v7",
		"Decimal":  "```\nDecimal\n```\n\nGo: `decimal.Decimal` | PG: `numeric` | MySQL: `DECIMAL`\n\nPrecise decimal arithmetic — use for money, never use Float for money\n\nAnnotations: `@decimal(precision, scale)`",
		"Bytes":    "```\nBytes\n```\n\nGo: `[]byte` | PG: `bytea` | MySQL: `BLOB` | Mongo: `BinData`\n\nBinary data storage — encrypted data, small files, certificates",
	}
	if desc, ok := descriptions[name]; ok {
		return desc
	}
	return ""
}

// ========== References ==========

func (s *Server) handleReferences(req *Request) error {
	var params ReferenceParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return err
	}

	doc := s.docs.Get(params.TextDocument.URI)
	if doc == nil {
		return s.transport.SendResponse(req.ID, nil)
	}

	word := doc.WordAt(params.Position)
	if word == "" {
		return s.transport.SendResponse(req.ID, nil)
	}

	var locations []Location
	for _, tok := range doc.Tokens {
		if tok.Val == word {
			locations = append(locations, Location{
				URI: params.TextDocument.URI,
				Range: Range{
					Start: Position{Line: tok.Pos.Line - 1, Character: tok.Pos.Col - 1},
					End:   Position{Line: tok.Pos.Line - 1, Character: tok.Pos.Col - 1 + len(word)},
				},
			})
		}
	}
	return s.transport.SendResponse(req.ID, locations)
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

	defPos := s.resolveDefinition(doc, word, params.Position)
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

func (s *Server) resolveDefinition(doc *Document, word string, pos Position) *token.Position {
	if p := resolveGlobalDefinition(doc, word); p != nil {
		return p
	}
	if doc.File == nil {
		return nil
	}
	return resolveLocalDefinition(doc, word, pos)
}

// resolveGlobalDefinition checks types, symbols, and enum values from semantic results.
func resolveGlobalDefinition(doc *Document, word string) *token.Position {
	if typ, ok := doc.Result.Types[word]; ok && typ.Pos.File != "" {
		return &typ.Pos
	}
	if sym := doc.Result.Scope.Lookup(word); sym != nil && sym.Pos.File != "" {
		return &sym.Pos
	}
	return findEnumValueDefinition(doc, word)
}

// findEnumValueDefinition searches all enum types for a matching value.
func findEnumValueDefinition(doc *Document, word string) *token.Position {
	for _, typ := range doc.Result.Types {
		if typ.Kind == semantic.TypeEnum {
			for _, v := range typ.EnumValues {
				if v == word {
					return &typ.Pos
				}
			}
		}
	}
	return nil
}

// resolveLocalDefinition checks position-dependent definitions (scope, my.load, member, local, CRUD).
func resolveLocalDefinition(doc *Document, word string, pos Position) *token.Position {
	if doc.IsInsideScopeDirective(pos) {
		if p := findScopeDefinition(doc, word); p != nil {
			return p
		}
	}
	if doc.IsInsideMyLoadCall(pos) {
		if p := findMyLoadFieldDefinition(doc, word); p != nil {
			return p
		}
	}
	if doc.IsNamedArgLabel(pos) {
		return resolveNamedArgDefinition(doc, word, pos)
	}
	if doc.IsAfterDot(pos) {
		if p := findMemberFieldDefinition(doc, word, pos); p != nil {
			return p
		}
	}
	if p := findLocalDefinition(doc.File, word, pos); p != nil {
		return p
	}
	return findCRUDBareFieldDefinition(doc, word, pos)
}

// findMyLoadFieldDefinition resolves an argument inside my.load(name, role) to the
// corresponding field definition in the @withAuth model.
func findMyLoadFieldDefinition(doc *Document, fieldName string) *token.Position {
	modelName := findWithAuthModelName(doc)
	if modelName == "" {
		return nil
	}
	// try AST-level lookup first (current file)
	if doc.File != nil {
		if p := findFieldInType(doc.File, modelName, fieldName); p != nil {
			return p
		}
	}
	// fall back to semantic result for cross-file resolution
	if doc.Result != nil {
		if typ, ok := doc.Result.Types[modelName]; ok {
			if fi, ok := typ.Fields[fieldName]; ok && fi.Pos.File != "" {
				return &fi.Pos
			}
		}
	}
	return nil
}

// findScopeDefinition searches all models in the AST for a scope with the given name.
func findScopeDefinition(doc *Document, scopeName string) *token.Position {
	if doc.File == nil {
		return nil
	}
	for _, m := range doc.File.Models {
		for _, sc := range m.Scopes {
			if sc.Name == scopeName {
				return &sc.Pos
			}
		}
	}
	return nil
}

// scopeNameHover returns a hover string for a scope name inside @scope(...).
func scopeNameHover(doc *Document, scopeName string) string {
	if doc.File == nil {
		return ""
	}
	for _, m := range doc.File.Models {
		for _, sc := range m.Scopes {
			if sc.Name == scopeName {
				var b strings.Builder
				b.WriteString("```luxo\n")
				fmt.Fprintf(&b, "scope %s", sc.Name)
				if len(sc.Params) > 0 {
					b.WriteByte('(')
					for i, p := range sc.Params {
						if i > 0 {
							b.WriteString(", ")
						}
						fmt.Fprintf(&b, "%s: %s", p.Name, p.Type.Name)
					}
					b.WriteByte(')')
				}
				b.WriteString("\n```\n")
				fmt.Fprintf(&b, "Defined in model `%s`\n\n", m.Name)
				fmt.Fprintf(&b, "定义在模型 `%s` 中", m.Name)
				return b.String()
			}
		}
	}
	return ""
}

// getScopeCompletions returns scope names from the API's return type model as completion items.
func (s *Server) getScopeCompletions(doc *Document, pos Position) []CompletionItem {
	modelName := findEnclosingApiReturnModelName(doc, pos)
	if modelName == "" {
		return nil
	}
	// find scopes defined on the target model
	for _, m := range doc.File.Models {
		if m.Name == modelName {
			items := make([]CompletionItem, 0, len(m.Scopes))
			for _, sc := range m.Scopes {
				detail := "scope " + sc.Name
				if len(sc.Params) > 0 {
					detail += "("
					for i, p := range sc.Params {
						if i > 0 {
							detail += ", "
						}
						detail += p.Name + ": " + typeRefToString(p.Type)
					}
					detail += ")"
				}
				items = append(items, CompletionItem{
					Label:    sc.Name,
					Kind:     20, // EnumMember — scope name acts like an enum value
					Detail:   detail,
					SortText: "a_" + sc.Name,
				})
			}
			return items
		}
	}
	return nil
}

// findEnclosingApiReturnModelName finds the API declaration on the same line as pos,
// and returns its return type's base model name (stripping [] for lists).
func findEnclosingApiReturnModelName(doc *Document, pos Position) string {
	if doc.File == nil {
		return ""
	}
	for _, api := range doc.File.APIs {
		// API declarations are typically on a single line or the directive is on the same line
		// Match when pos.Line equals the API's declaration line (0-based vs 1-based)
		if api.Pos.Line-1 == pos.Line {
			if api.ReturnType == nil {
				return ""
			}
			return api.ReturnType.Name
		}
	}
	return ""
}

// findWithAuthModelName finds the model name that has the @withAuth directive
// by searching the current file's AST model declarations.
func findWithAuthModelName(doc *Document) string {
	if doc.File == nil {
		return ""
	}
	for _, m := range doc.File.Models {
		for _, d := range m.Directives {
			if d.Name == "withAuth" {
				return m.Name
			}
		}
	}
	return ""
}

// findCRUDBareFieldDefinition resolves a bare field name (without "it." prefix) inside a CRUD call
// to the target model's field definition.
// e.g., find(RoomMember, where: userId == my.id) — "userId" jumps to RoomMember.userId.
func findCRUDBareFieldDefinition(doc *Document, fieldName string, pos Position) *token.Position {
	funcName, modelName := doc.FindEnclosingCall(pos)
	if !isCRUDFunc(funcName) || modelName == "" {
		return nil
	}
	if doc.File == nil {
		return nil
	}
	return findFieldInType(doc.File, modelName, fieldName)
}

// resolveNamedArgDefinition resolves a named arg key to a field definition.
func resolveNamedArgDefinition(doc *Document, word string, pos Position) *token.Position {
	if p := findCRUDFieldDefinition(doc, word, pos); p != nil {
		return p
	}
	// named arg keys in CRUD calls are query parameters, not variable references
	funcName, _ := doc.FindEnclosingCall(pos)
	if isCRUDFunc(funcName) {
		return nil
	}
	if p := findObjectFieldDefinition(doc, word, pos); p != nil {
		return p
	}
	return nil
}

// findMemberFieldDefinition resolves member access to a type's field definition.
func findMemberFieldDefinition(doc *Document, fieldName string, pos Position) *token.Position {
	if doc.File == nil || doc.Result == nil {
		return nil
	}
	objName := doc.ObjectBeforeDot(pos)
	if objName == "" {
		return nil
	}
	// "it" is lambda implicit param — resolve against CRUD model if in CRUD context
	if objName == "it" {
		funcName, modelName := doc.FindEnclosingCall(pos)
		if isCRUDFunc(funcName) && modelName != "" {
			if p := findFieldInType(doc.File, modelName, fieldName); p != nil {
				return p
			}
		}
		return findFieldInAnyType(doc.File, fieldName)
	}
	typeName := findParamTypeName(doc.File, objName)
	if typeName == "" {
		typeName = findValTypeName(doc.File, objName, pos)
	}
	if typeName == "" {
		return nil
	}
	return findFieldInType(doc.File, typeName, fieldName)
}

// findValTypeName searches for a val statement matching varName in the enclosing
// api/fn body, and infers the type name from the assigned expression.
func findValTypeName(file *ast.File, varName string, cursor Position) string {
	// search api bodies
	for _, api := range file.APIs {
		if !posInsideDecl(cursor, api.Pos, api.Body) {
			continue
		}
		if api.Body != nil {
			if tn := findValTypeInBlock(api.Body, varName, cursor); tn != "" {
				return tn
			}
		}
	}
	// search fn bodies
	for _, fn := range file.Functions {
		if !posInsideDecl(cursor, fn.Pos, fn.Body) {
			continue
		}
		if fn.Body != nil {
			if tn := findValTypeInBlock(fn.Body, varName, cursor); tn != "" {
				return tn
			}
		}
	}
	return ""
}

// findValTypeInBlock finds a val statement for varName before cursor and infers its type.
func findValTypeInBlock(block *ast.Block, varName string, cursor Position) string {
	for _, stmt := range block.Stmts {
		if stmt.GetPos().Line-1 > cursor.Line {
			break
		}
		vs, ok := stmt.(*ast.ValStmt)
		if !ok || vs.Name != varName {
			continue
		}
		// explicit type annotation
		if vs.Type != nil {
			return vs.Type.Name
		}
		// infer from assigned expression
		return inferExprTypeName(vs.Value)
	}
	return ""
}

// inferExprTypeName extracts the model/type name from an expression.
// Handles: create(Order, ...), find(Order, ...), transaction { ... create(Order, ...) }, etc.
func inferExprTypeName(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		return inferCallTypeName(e)
	case *ast.TransactionExpr:
		return inferBlockTypeName(e.Body)
	}
	return ""
}

// inferCallTypeName extracts the model name from a CRUD call.
// Supports both function-style create(Order, ...) and chain-style Order.create(...).
func inferCallTypeName(call *ast.CallExpr) string {
	// function-style: create(Order, ...), find(Order, ...)
	if fn, ok := call.Func.(*ast.Ident); ok {
		if isCRUDFunc(fn.Name) {
			if len(call.Args) == 0 {
				return ""
			}
			if ident, ok := call.Args[0].Value.(*ast.Ident); ok {
				return ident.Name
			}
			return ""
		}
		// block-calls like transaction { ... } parsed as CallExpr with trailing LambdaExpr
		if len(call.Args) > 0 {
			lastArg := call.Args[len(call.Args)-1]
			if lambda, ok := lastArg.Value.(*ast.LambdaExpr); ok {
				return inferBlockTypeName(lambda.Body)
			}
		}
		return ""
	}

	// chain-style: Order.create(...), User.find(...)
	if member, ok := call.Func.(*ast.MemberExpr); ok && isCRUDFunc(member.Field) {
		if ident, ok := member.Object.(*ast.Ident); ok {
			return ident.Name
		}
	}

	return ""
}

// inferBlockTypeName infers the type from the last expression in a block.
func inferBlockTypeName(block *ast.Block) string {
	if block == nil || len(block.Stmts) == 0 {
		return ""
	}
	last := block.Stmts[len(block.Stmts)-1]
	// ExprStmt wrapping an expression
	if es, ok := last.(*ast.ExprStmt); ok {
		return inferExprTypeName(es.Expr)
	}
	// ValStmt as last statement — infer from its value
	if vs, ok := last.(*ast.ValStmt); ok {
		if vs.Type != nil {
			return vs.Type.Name
		}
		return inferExprTypeName(vs.Value)
	}
	return ""
}

// findFieldInAnyType searches all models and types for a field by name.
func findFieldInAnyType(file *ast.File, fieldName string) *token.Position {
	for _, m := range file.Models {
		for _, f := range m.Fields {
			if f.Name == fieldName {
				return &f.Pos
			}
		}
	}
	for _, t := range file.Types {
		for _, f := range t.Fields {
			if f.Name == fieldName {
				return &f.Pos
			}
		}
	}
	return nil
}

// findFieldInType looks up a field by name in models and types.
func findFieldInType(file *ast.File, typeName, fieldName string) *token.Position {
	for _, m := range file.Models {
		if m.Name == typeName {
			for _, f := range m.Fields {
				if f.Name == fieldName {
					return &f.Pos
				}
			}
		}
	}
	for _, t := range file.Types {
		if t.Name == typeName {
			for _, f := range t.Fields {
				if f.Name == fieldName {
					return &f.Pos
				}
			}
		}
	}
	return findFieldInSealed(file.Sealeds, typeName, fieldName)
}

// findFieldInSealed looks up a field by name in sealed types and their variants.
func findFieldInSealed(sealeds []*ast.SealedDecl, typeName, fieldName string) *token.Position {
	for _, s := range sealeds {
		if s.Name == typeName {
			if p := findFieldInVariants(s.Variants, fieldName); p != nil {
				return p
			}
		}
		for _, v := range s.Variants {
			if v.Name == typeName {
				return findParamByName(v.Fields, fieldName)
			}
		}
	}
	return nil
}

// findFieldInVariants searches all variants of a sealed type for a field.
func findFieldInVariants(variants []*ast.SealedVariant, fieldName string) *token.Position {
	for _, v := range variants {
		if p := findParamByName(v.Fields, fieldName); p != nil {
			return p
		}
	}
	return nil
}

// findParamByName finds a parameter by name in a slice of ParamDecl.
func findParamByName(params []*ast.ParamDecl, name string) *token.Position {
	for _, f := range params {
		if f.Name == name {
			return &f.Pos
		}
	}
	return nil
}

// findCRUDFieldDefinition resolves a named arg label in a CRUD call to a model field definition.
// e.g., find(Order, id: id) — clicking on "id:" jumps to Order.id field.
func findCRUDFieldDefinition(doc *Document, fieldName string, pos Position) *token.Position {
	funcName, modelName := doc.FindEnclosingCall(pos)
	if !isCRUDFunc(funcName) || modelName == "" {
		return nil
	}
	// CRUD params like "where", "id", "orderBy" are not model fields
	if isCRUDParam(fieldName) {
		return nil
	}
	if doc.File == nil {
		return nil
	}
	for _, m := range doc.File.Models {
		if m.Name == modelName {
			for _, f := range m.Fields {
				if f.Name == fieldName {
					return &f.Pos
				}
			}
		}
	}
	return nil
}

// findObjectFieldDefinition resolves a named arg label in an object construction
// to a type's field definition.
// e.g., AuthResult { user: currentUser } — clicking on "user:" jumps to AuthResult.user field.
func findObjectFieldDefinition(doc *Document, fieldName string, pos Position) *token.Position {
	typeName := doc.FindEnclosingObjectType(pos)
	if typeName == "" {
		return nil
	}
	if doc.File != nil {
		if p := findFieldInType(doc.File, typeName, fieldName); p != nil {
			return p
		}
	}
	return nil
}

// getCRUDCompletions provides CRUD parameter and model field completions.
func (s *Server) getCRUDCompletions(doc *Document, pos Position) []CompletionItem {
	funcName, modelName := doc.FindEnclosingCall(pos)
	if !isCRUDFunc(funcName) || modelName == "" {
		return nil
	}
	var items []CompletionItem

	// CRUD operation params
	items = append(items, getCRUDParamCompletions(funcName)...)

	// model field completions for where/create/update context
	if doc.Result != nil {
		if typ, ok := doc.Result.Types[modelName]; ok && typ.Fields != nil {
			for fieldName, field := range typ.Fields {
				detail := "field"
				if field.Type != nil {
					detail = field.Type.Name
				}
				items = append(items, CompletionItem{
					Label:  fieldName,
					Kind:   5, // Field
					Detail: detail,
				})
			}
		}
	}
	return items
}

func getCRUDParamCompletions(funcName string) []CompletionItem {
	var items []CompletionItem
	switch funcName {
	case "find":
		items = append(items,
			CompletionItem{Label: "id", Kind: 5, Detail: "id: — find by primary key / 按主键查找"},
			CompletionItem{Label: "where", Kind: 5, Detail: "where: — filter condition / 过滤条件"},
			CompletionItem{Label: "orderBy", Kind: 5, Detail: "orderBy: — sort order / 排序"},
			CompletionItem{Label: "limit", Kind: 5, Detail: "limit: — max results / 最大结果数"},
			CompletionItem{Label: "offset", Kind: 5, Detail: "offset: — skip results / 跳过条数"},
			CompletionItem{Label: "first", Kind: 5, Detail: "first: — first N results / 前 N 条"},
			CompletionItem{Label: "last", Kind: 5, Detail: "last: — last N results / 后 N 条"},
		)
	case "create":
		// model fields are the params — no extra keywords
	case "update":
		items = append(items,
			CompletionItem{Label: "where", Kind: 5, Detail: "where: — update condition / 更新条件"},
		)
	case "delete":
		items = append(items,
			CompletionItem{Label: "where", Kind: 5, Detail: "where: — delete condition / 删除条件"},
		)
	}
	return items
}

func isCRUDFunc(name string) bool {
	switch name {
	case "find", "create", "update", "delete":
		return true
	}
	return false
}

func isCRUDParam(name string) bool {
	switch name {
	case "where", "id", "orderBy", "limit", "offset", "first", "last":
		return true
	}
	return false
}

// findLocalDefinition searches api/fn params and body val statements for a local variable definition.
func findLocalDefinition(file *ast.File, word string, pos Position) *token.Position {
	// search api params and body
	for _, api := range file.APIs {
		if !posInsideDecl(pos, api.Pos, api.Body) {
			continue
		}
		for _, p := range api.Params {
			if p.Name == word {
				return &p.Pos
			}
		}
		if api.Body != nil {
			if found := findValInBlock(api.Body, word, pos); found != nil {
				return found
			}
		}
	}
	// search fn params and body
	for _, fn := range file.Functions {
		if !posInsideDecl(pos, fn.Pos, fn.Body) {
			continue
		}
		for _, p := range fn.Params {
			if p.Name == word {
				return &p.Pos
			}
		}
		if fn.Body != nil {
			if found := findValInBlock(fn.Body, word, pos); found != nil {
				return found
			}
		}
	}
	return nil
}

// posInsideDecl checks if cursor position is within a declaration's range.
func posInsideDecl(cursor Position, declPos token.Position, body *ast.Block) bool {
	if cursor.Line < declPos.Line-1 {
		return false
	}
	if body != nil && body.EndPos.Line > 0 {
		return cursor.Line <= body.EndPos.Line-1
	}
	return false
}

// findValInBlock searches a block for a val statement or for-loop variable matching word, before cursor.
func findValInBlock(block *ast.Block, word string, cursor Position) *token.Position {
	for _, stmt := range block.Stmts {
		// only consider definitions before cursor
		if stmt.GetPos().Line-1 > cursor.Line {
			break
		}
		if vs, ok := stmt.(*ast.ValStmt); ok {
			if vs.Name == word {
				return &vs.Pos
			}
			for _, name := range vs.Names {
				if name == word {
					return &vs.Pos
				}
			}
		}
		if fs, ok := stmt.(*ast.ForStmt); ok {
			if fs.VarName == word {
				return &fs.Pos
			}
		}
	}
	return nil
}

// ========== Formatting ==========

func (s *Server) handleFormatting(req *Request) error {
	var params FormattingParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.transport.SendError(req.ID, -32602, "invalid params")
	}

	doc := s.docs.Get(params.TextDocument.URI)
	if doc == nil {
		return s.transport.SendResponse(req.ID, nil)
	}

	source := doc.Content
	var formatted string
	if len(doc.Tokens) > 0 && len(doc.lexErrors) == 0 {
		// Reuse existing token stream from analysis, avoiding redundant lexing.
		formatted = formatter.FormatTokens(doc.Tokens)
	} else {
		var err error
		formatted, err = formatter.Format(source, params.TextDocument.URI)
		if err != nil {
			return s.transport.SendResponse(req.ID, nil)
		}
	}

	if formatted == source {
		return s.transport.SendResponse(req.ID, nil)
	}

	// Replace entire document
	lines := strings.Count(source, "\n")
	lastLineLen := len(source)
	if idx := strings.LastIndexByte(source, '\n'); idx >= 0 {
		lastLineLen = len(source) - idx - 1
	}

	edits := []TextEdit{{
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: lines, Character: lastLineLen},
		},
		NewText: formatted,
	}}
	return s.transport.SendResponse(req.ID, edits)
}

// ========== Inlay Hints ==========

func (s *Server) handleInlayHint(req *Request) error {
	var params InlayHintParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.transport.SendError(req.ID, -32602, "invalid params")
	}

	doc := s.docs.Get(params.TextDocument.URI)
	if doc == nil || doc.File == nil {
		return s.transport.SendResponse(req.ID, []InlayHint{})
	}

	hints := collectImplicitReturnHints(doc.File)
	return s.transport.SendResponse(req.ID, hints)
}

// collectImplicitReturnHints finds the last expression in api/fn bodies
// that serves as an implicit return value.
func collectImplicitReturnHints(file *ast.File) []InlayHint {
	var hints []InlayHint

	for _, api := range file.APIs {
		if api.Body != nil && api.ReturnType != nil {
			if h := findImplicitReturn(api.Body); h != nil {
				hints = append(hints, *h)
			}
		}
	}

	for _, fn := range file.Functions {
		if fn.Body != nil && fn.ReturnType != nil {
			if h := findImplicitReturn(fn.Body); h != nil {
				hints = append(hints, *h)
			}
		}
	}

	return hints
}

// findImplicitReturn checks if the last statement in a block is an
// expression statement (implicit return) and returns an InlayHint for it.
func findImplicitReturn(block *ast.Block) *InlayHint {
	if block == nil || len(block.Stmts) == 0 {
		return nil
	}

	last := block.Stmts[len(block.Stmts)-1]
	exprStmt, ok := last.(*ast.ExprStmt)
	if !ok {
		return nil
	}

	pos := exprStmt.Pos
	return &InlayHint{
		Position:     Position{Line: pos.Line - 1, Character: pos.Col - 1},
		Label:        "return ",
		Kind:         1, // Type hint
		Tooltip:      "implicit return / 隐式返回",
		PaddingRight: true,
	}
}
