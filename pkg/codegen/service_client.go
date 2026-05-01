package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// serviceFnInfo describes a fn @service for client stub generation.
type serviceFnInfo struct {
	moduleName string
	fnName     string
	params     []*ast.ParamDecl
	returnType *ast.TypeRef
}

// collectRemoteServiceFns finds fn @service declarations in OTHER modules.
// Used to generate client stubs for calling remote services.
func collectRemoteServiceFns(result *semantic.Result) map[string][]serviceFnInfo {
	// Group service fns by module
	byModule := make(map[string][]serviceFnInfo)
	for _, file := range result.Files {
		modName := moduleNameFromFile(file.Name)
		for _, fn := range file.Functions {
			if !hasDirective(fn.Directives, "service") {
				continue
			}
			byModule[modName] = append(byModule[modName], serviceFnInfo{
				moduleName: modName,
				fnName:     fn.Name,
				params:     fn.Params,
				returnType: fn.ReturnType,
			})
		}
	}
	return byModule
}

// generateServiceClientFile produces service_client.gen.go containing
// type-safe RPC client stubs for calling fn @service in other modules.
// Returns nil if no remote service fns exist.
func generateServiceClientFile(result *semantic.Result, packageName string) []byte {
	byModule := collectRemoteServiceFns(result)
	currentModule := "" // TODO: determine current module from package name
	_ = currentModule

	// Collect all modules that have service fns
	var modules []string
	for mod := range byModule {
		modules = append(modules, mod)
	}
	if len(modules) == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "service_client.gen.go")
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/rpc\"\n")
	b.WriteString(")\n\n")

	for _, mod := range modules {
		fns := byModule[mod]
		generateServiceClient(&b, mod, fns)
	}

	return []byte(b.String())
}

// generateServiceClient generates a type-safe RPC client for one module's service fns.
func generateServiceClient(b *strings.Builder, moduleName string, fns []serviceFnInfo) {
	clientName := str.Capitalize(moduleName) + "ServiceClient"

	fmt.Fprintf(b, "// %s provides type-safe RPC calls to %s module's fn @service endpoints.\n", clientName, moduleName)
	fmt.Fprintf(b, "type %s struct {\n", clientName)
	fmt.Fprintf(b, "\tclient *rpc.Client\n")
	fmt.Fprintf(b, "}\n\n")

	fmt.Fprintf(b, "// New%s creates a client for the %s service.\n", clientName, moduleName)
	fmt.Fprintf(b, "func New%s(addr string) *%s {\n", clientName, clientName)
	fmt.Fprintf(b, "\treturn &%s{client: rpc.NewClient(addr)}\n", clientName)
	fmt.Fprintf(b, "}\n\n")

	fmt.Fprintf(b, "// Close closes the RPC connection.\n")
	fmt.Fprintf(b, "func (c *%s) Close() {\n", clientName)
	fmt.Fprintf(b, "\tc.client.Close()\n")
	fmt.Fprintf(b, "}\n\n")

	for _, fn := range fns {
		generateServiceMethod(b, clientName, fn)
	}
}

// generateServiceMethod generates a single RPC client method.
func generateServiceMethod(b *strings.Builder, clientName string, fn serviceFnInfo) {
	methodName := str.Capitalize(fn.fnName)
	retType := "any"
	if fn.returnType != nil {
		retType = resolveGoType(fn.returnType)
	}

	// Method signature
	fmt.Fprintf(b, "// %s calls svc:%s on the %s service.\n", methodName, fn.fnName, fn.moduleName)
	fmt.Fprintf(b, "func (c *%s) %s(ctx context.Context", clientName, methodName)
	for _, p := range fn.params {
		goType := resolveGoType(p.Type)
		fmt.Fprintf(b, ", %s %s", p.Name, goType)
	}
	fmt.Fprintf(b, ") (%s, error) {\n", retType)

	// Encode params
	fmt.Fprintf(b, "\tparams := rpc.EncodeParams(\n")
	for i, p := range fn.params {
		fmt.Fprintf(b, "\t\trpc.ParamField{FieldID: %d, Value: %s},\n", i+1, p.Name)
	}
	fmt.Fprintf(b, "\t)\n")

	// Call RPC
	svcName := "svc:" + fn.fnName
	fmt.Fprintf(b, "\tresp, err := c.client.Call(0, params) // %s\n", svcName)
	fmt.Fprintf(b, "\tif err != nil {\n")
	fmt.Fprintf(b, "\t\tvar zero %s\n", retType)
	fmt.Fprintf(b, "\t\treturn zero, fmt.Errorf(\"rpc %s: %%w\", err)\n", svcName)
	fmt.Fprintf(b, "\t}\n")

	// Decode response based on return type
	switch retType {
	case "int64":
		fmt.Fprintf(b, "\tv, n := codec.ReadSvarint(resp, 0)\n")
		fmt.Fprintf(b, "\tif n <= 0 {\n\t\treturn 0, fmt.Errorf(\"rpc %s: invalid int response\")\n\t}\n", svcName)
		fmt.Fprintf(b, "\treturn v, nil\n")
	case "float64":
		fmt.Fprintf(b, "\tv, n := codec.ReadFixed64(resp, 0)\n")
		fmt.Fprintf(b, "\tif n == 0 {\n\t\treturn 0, fmt.Errorf(\"rpc %s: invalid float response\")\n\t}\n", svcName)
		fmt.Fprintf(b, "\treturn v, nil\n")
	case "string":
		fmt.Fprintf(b, "\tv, n := codec.ReadString(resp, 0)\n")
		fmt.Fprintf(b, "\tif n == 0 {\n\t\treturn \"\", fmt.Errorf(\"rpc %s: invalid string response\")\n\t}\n", svcName)
		fmt.Fprintf(b, "\treturn v, nil\n")
	case "bool":
		fmt.Fprintf(b, "\tv, n := codec.ReadBool(resp, 0)\n")
		fmt.Fprintf(b, "\tif n == 0 {\n\t\treturn false, fmt.Errorf(\"rpc %s: invalid bool response\")\n\t}\n", svcName)
		fmt.Fprintf(b, "\treturn v, nil\n")

	default:
		// Model type — decode with ReadLuxo
		fmt.Fprintf(b, "\tvar result %s\n", retType)
		fmt.Fprintf(b, "\tdec := codec.NewDecoder(resp)\n")
		fmt.Fprintf(b, "\tresult.ReadLuxo(dec)\n")
		fmt.Fprintf(b, "\tif dec.Err() != nil {\n\t\treturn result, dec.Err()\n\t}\n")
		fmt.Fprintf(b, "\treturn result, nil\n")
	}
	fmt.Fprintf(b, "}\n\n")
}
