package semantic

import "github.com/light-speak/luxo/pkg/ast"

// Bind the declared subject once, before bodies are checked or code is emitted.
func (a *Analyzer) bindDefaultAuthSubject(files []*ast.File, selected *ast.ModelDecl, ambiguous bool) {
	subject := ""
	if selected != nil {
		subject = selected.Name
	}
	a.defaultAuthSubject = subject
	for _, file := range files {
		for _, model := range file.Models {
			a.bindAuthDirectives(model.Directives, subject, ambiguous)
			for _, d := range model.Directives {
				if d.Name == "withAuth" {
					d.AuthSubject = model.Name
				}
			}
		}
		for _, api := range file.APIs {
			a.bindAuthDirectives(api.Directives, subject, ambiguous)
		}
		for _, fn := range file.Functions {
			a.bindAuthDirectives(fn.Directives, subject, ambiguous)
		}
	}
}

func (a *Analyzer) bindAuthDirectives(directives []*ast.Directive, subject string, ambiguous bool) {
	for _, d := range directives {
		if d.Name != "auth" {
			continue
		}
		d.AuthSubject = subject
		explicit := false
		for _, arg := range d.Args {
			if arg.Name != "subject" {
				continue
			}
			explicit = true
			value, ok := arg.Value.(*ast.Ident)
			if !ok {
				continue
			}
			if a.authIdentities[value.Name] == nil {
				a.addError(d.Pos, "invalid auth subject %q: requires a @withAuth model / 认证主体必须是 @withAuth 模型", value.Name)
				continue
			}
			d.AuthSubject = value.Name
		}
		if !explicit && subject == "" && ambiguous {
			a.addError(d.Pos, "ambiguous auth subject: declare a default @withAuth model / 多个认证主体需要声明默认主体")
		}
	}
}

// Each subject has its own stored-field types; no cross-subject field union.
func (a *Analyzer) prepareAuthIdentities(files []*ast.File) {
	a.authIdentities = make(map[string]*ResolvedType)
	for _, file := range files {
		for _, model := range file.Models {
			if !hasModelDirective(model.Directives, "withAuth") {
				continue
			}
			identity := newIdentityType()
			injectIdentityStores(identity, a.types[model.Name], model.Directives)
			if identity.Fields["id"].Type == nil {
				identity.Fields["id"].Type = a.types["Int"]
			}
			if identity.Fields["role"].Type == nil {
				identity.Fields["role"].Type = a.types["String"]
			}
			a.authIdentities[model.Name] = identity
		}
	}
}

func (a *Analyzer) authIdentity(directives []*ast.Directive) *ResolvedType {
	for _, d := range directives {
		if d.Name == "auth" && a.authIdentities[d.AuthSubject] != nil {
			return a.authIdentities[d.AuthSubject]
		}
	}
	return a.types["Identity"]
}

func (a *Analyzer) bindVisibleSubject(directive *ast.Directive, expression ast.Expr) {
	usesIdentity := false
	ast.WalkExpr(expression, func(value ast.Expr) {
		if ident, ok := value.(*ast.Ident); ok && ident.Name == "my" {
			usesIdentity = true
		}
	})
	directive.AuthSubject = ""
	if !usesIdentity {
		return
	}
	directive.AuthSubject = a.defaultAuthSubject
	if directive.AuthSubject == "" && len(a.authIdentities) > 1 {
		a.addError(directive.Pos, "ambiguous auth subject for @visible: declare a default model / 字段可见性需要明确默认认证主体")
	}
}
