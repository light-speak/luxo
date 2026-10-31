package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

func validateAuthStores(result *semantic.Result) error {
	for _, file := range result.Files {
		for _, model := range file.Models {
			directive := findDirective(model.Directives, "withAuth")
			if directive == nil {
				continue
			}
			if err := validateAuthStoreFields(model, directive); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAuthStoreFields(model *ast.ModelDecl, directive *ast.Directive) error {
	var list *ast.ListExpr
	for _, arg := range directive.Args {
		if arg.Name == "stores" {
			list, _ = arg.Value.(*ast.ListExpr)
		}
	}
	if list == nil {
		return fmt.Errorf("model %s @withAuth requires an explicit stores field list", model.Name)
	}
	fields := make(map[string]*ast.FieldDecl, len(model.Fields))
	for _, field := range model.Fields {
		fields[field.Name] = field
	}
	seen := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		ident, ok := item.(*ast.Ident)
		if !ok {
			return fmt.Errorf("model %s @withAuth stores requires field names", model.Name)
		}
		field := fields[ident.Name]
		if field == nil || field.Type == nil {
			return fmt.Errorf("model %s @withAuth stores has unknown or untyped field %s", model.Name, ident.Name)
		}
		if seen[ident.Name] {
			return fmt.Errorf("model %s @withAuth stores repeats field %s", model.Name, ident.Name)
		}
		seen[ident.Name] = true
	}
	return nil
}

func authStoreFields(m *ast.ModelDecl) []*ast.FieldDecl {
	var fields []*ast.FieldDecl
	directive := findDirective(m.Directives, "withAuth")
	if directive == nil {
		return nil
	}
	for _, arg := range directive.Args {
		if arg.Name != "stores" {
			continue
		}
		list, ok := arg.Value.(*ast.ListExpr)
		if !ok {
			continue
		}
		for _, item := range list.Items {
			ident, ok := item.(*ast.Ident)
			if !ok {
				continue
			}
			for _, field := range m.Fields {
				if field.Name == ident.Name {
					fields = append(fields, field)
					break
				}
			}
		}
	}
	return fields
}

func generateAuthMethods(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, writers []*errorDataGenerator) {
	if !hasDirective(m.Directives, "withAuth") {
		return
	}
	var writer *errorDataGenerator
	if len(writers) != 0 {
		writer = writers[0]
	} else {
		writer = newErrorDataGenerator(&semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{m}}}}, defaultGenerator())
		writer.enums = enums
		writer.objectPrefix = "Auth"
	}
	recv := strings.ToLower(m.Name[:1])
	fields := authStoreFields(m)
	writeAuthClaims(b, m, fields, recv, writer)
	writeAuthIssuers(b, m.Name, recv)
	fmt.Fprintf(b, "\nfunc (%s *%s) RefreshToken(oldToken string) (string, error) {\n", recv, m.Name)
	b.WriteString("\tcfg, err := auth.LoadConfig()\n")
	b.WriteString("\tif err != nil { return \"\", err }\n")
	b.WriteString("\tdata, err := auth.VerifyRefresh(cfg, oldToken)\n")
	b.WriteString("\tif err != nil { return \"\", err }\n")
	b.WriteString("\treturn auth.Sign(cfg, data.AppendJSON)\n}\n")
	if len(writers) == 0 {
		writer.writeObjects(b)
	}
}

func writeAuthIssuers(b *strings.Builder, model, recv string) {
	for _, method := range []struct{ name, params, sign, extra string }{
		{"CreateToken", "", "Sign", ""},
		{"CreateTokenWithExpiry", "expires time.Duration", "SignWithExpiry", ", expires"},
		{"CreateRefreshToken", "", "SignRefresh", ""},
	} {
		fmt.Fprintf(b, "\nfunc (%s *%s) %s(%s) (string, error) {\n", recv, model, method.name, method.params)
		b.WriteString("\tcfg, err := auth.LoadConfig()\n")
		b.WriteString("\tif err != nil { return \"\", err }\n")
		fmt.Fprintf(b, "\treturn auth.%s(cfg, %s.AppendClaimsJSON%s)\n}\n", method.sign, recv, method.extra)
	}
}

func writeAuthClaims(b *strings.Builder, m *ast.ModelDecl, fields []*ast.FieldDecl, recv string, writer *errorDataGenerator) {
	fmt.Fprintf(b, "\nfunc (%s *%s) AppendClaimsJSON(dst []byte) ([]byte, error) {\n", recv, m.Name)
	b.WriteString("\tw := errors.NewDataWriter(dst)\n")
	fmt.Fprintf(b, "\tif %s == nil { w.Reject(\"JWT identity is nil\"); return w.Result() }\n", recv)
	b.WriteString("\tw.Token(\"{\")\n")
	for i, field := range fields {
		writeErrorFieldName(b, field.Name, i, "\t")
		fmt.Fprintf(b, "\t%s.appendClaim%d(&w)\n", recv, i)
	}
	b.WriteString("\tw.Token(\"}\")\n\treturn w.Result()\n}\n")
	for i, field := range fields {
		fmt.Fprintf(b, "\nfunc (%s *%s) appendClaim%d(w *errors.DataWriter) {\n", recv, m.Name, i)
		b.WriteString("\tif w.Err() != nil { return }\n")
		writer.writeValue(b, field.Type, recv+"."+str.Capitalize(field.Name), isRelationField(field, writer.enums), false, "\t")
		b.WriteString("}\n")
	}
}
