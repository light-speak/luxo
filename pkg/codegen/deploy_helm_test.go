package codegen

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"text/template"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
	"gopkg.in/yaml.v3"
)

func TestHelmReferencesExistingJWTSecretForApplicationServices(t *testing.T) {
	helm := GenerateHelmFiles(makeHelmResult("user", "post"), "myapp")
	var values struct {
		JWT struct {
			ExistingSecret *string `yaml:"existingSecret"`
		} `yaml:"jwt"`
	}
	if err := yaml.Unmarshal(helm.Files["values.yaml"], &values); err != nil {
		t.Fatal(err)
	}
	if values.JWT.ExistingSecret == nil || *values.JWT.ExistingSecret != "" {
		t.Fatal("the JWT Secret must be opt-in")
	}
	for _, name := range []string{"gateway", "module-user", "module-post"} {
		deployment := string(helm.Files["templates/"+name+"-deployment.yaml"])
		if !strings.Contains(deployment, "include \"luxo.jwtEnvironment\" . | nindent 12 }}\n          envFrom:") {
			t.Errorf("%s does not use the JWT Secret helper in the container environment", name)
		}
	}
	if bytes.Contains(helm.Files["templates/secret.yaml"], []byte("JWT_SECRET")) {
		t.Fatal("the chart must not create its own JWT signing key")
	}
	const definition = "{{- define \"luxo.jwtEnvironment\" -}}"
	_, body, found := strings.Cut(string(helm.Files["templates/_helpers.tpl"]), definition)
	if !found {
		t.Fatal("missing the JWT Secret helper definition")
	}
	tmpl, err := template.New("auth").Funcs(template.FuncMap{"quote": strconv.Quote}).Parse(definition + body)
	if err != nil {
		t.Fatal(err)
	}
	checkHelmJWTEnvironment(t, tmpl, "")
	checkHelmJWTEnvironment(t, tmpl, "application-jwt")
}

func checkHelmJWTEnvironment(t *testing.T, tmpl *template.Template, secret string) {
	t.Helper()
	var rendered bytes.Buffer
	values := map[string]any{"Values": map[string]any{"jwt": map[string]string{"existingSecret": secret}}}
	if err := tmpl.ExecuteTemplate(&rendered, "luxo.jwtEnvironment", values); err != nil {
		t.Fatal(err)
	}
	var environment []struct {
		Name      string
		ValueFrom struct {
			SecretKeyRef struct {
				Name     string
				Key      string
				Optional bool
			} `yaml:"secretKeyRef"`
		} `yaml:"valueFrom"`
	}
	if err := yaml.Unmarshal(rendered.Bytes(), &environment); err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		if len(environment) != 0 {
			t.Fatalf("JWT is disabled, found env: %s", rendered.String())
		}
		return
	}
	keys := []string{"JWT_SECRET", "JWT_EXPIRES", "JWT_REFRESH", "JWT_REFRESH_EXPIRES"}
	if len(environment) != len(keys) {
		t.Fatalf("incorrect JWT env: %s", rendered.String())
	}
	for i, key := range keys {
		entry := environment[i]
		ref := entry.ValueFrom.SecretKeyRef
		if entry.Name != key || ref.Key != key || ref.Name != secret || ref.Optional != (key != "JWT_SECRET") {
			t.Errorf("incorrect Secret reference for %s: %+v", key, entry)
		}
	}
}

func TestHelmConditionalResourceHeader(t *testing.T) {
	helm := GenerateHelmFiles(makeHelmResult("user"), "probe")
	for _, sample := range []struct {
		file, flag, option, apiVersion, kind string
	}{
		{"serviceaccount.yaml", "serviceAccount", "create", "v1", "ServiceAccount"},
		{"ingress.yaml", "ingress", "enabled", "networking.k8s.io/v1", "Ingress"},
	} {
		for _, enabled := range []bool{false, true} {
			label := "disabled"
			if enabled {
				label = "enabled"
			}
			t.Run(sample.file+"/"+label, func(t *testing.T) {
				source := helm.Files["templates/"+sample.file]
				end := bytes.Index(source, []byte("\nmetadata:"))
				if end < 0 {
					t.Fatal("generated resource has no metadata boundary")
				}
				// Header actions use Go's template engine; full Helm consumption is checked separately.
				header, err := template.New("header").Parse(string(source[:end]) + "\n{{- end }}\n")
				if err != nil {
					t.Fatal(err)
				}
				values := map[string]map[string]map[string]bool{"Values": {sample.flag: {sample.option: enabled}}}
				var rendered bytes.Buffer
				if err := header.Execute(&rendered, values); err != nil {
					t.Fatal(err)
				}
				var resource struct {
					APIVersion string `yaml:"apiVersion"`
					Kind       string `yaml:"kind"`
				}
				if err := yaml.Unmarshal(rendered.Bytes(), &resource); err != nil {
					t.Fatal(err)
				}
				if enabled && (resource.APIVersion != sample.apiVersion || resource.Kind != sample.kind) || !enabled && (resource.APIVersion != "" || resource.Kind != "") {
					t.Fatalf("conditional resource header is invalid: %+v\n%s", resource, rendered.String())
				}
			})
		}
	}
}

func makeHelmResult(modules ...string) *semantic.Result {
	var files []*ast.File
	for _, name := range modules {
		files = append(files, &ast.File{
			Name: "origin/" + name + ".luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       strings.Title(name),
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
		})
	}
	return &semantic.Result{Files: files}
}

func TestHelmGenerateFiles_Basic(t *testing.T) {
	result := makeHelmResult("user", "post")
	helm := GenerateHelmFiles(result, "myapp")
	if helm == nil {
		t.Fatal("should generate helm files")
	}

	// Verify all expected files exist
	expectedFiles := []string{
		"Chart.yaml",
		"values.yaml",
		".helmignore",
		"templates/_helpers.tpl",
		"templates/configmap.yaml",
		"templates/secret.yaml",
		"templates/serviceaccount.yaml",
		"templates/gateway-deployment.yaml",
		"templates/gateway-service.yaml",
		"templates/ingress.yaml",
		"templates/hpa.yaml",
		"templates/module-user-deployment.yaml",
		"templates/module-user-service.yaml",
		"templates/module-post-deployment.yaml",
		"templates/module-post-service.yaml",
	}
	for _, f := range expectedFiles {
		if _, ok := helm.Files[f]; !ok {
			t.Errorf("missing file: %s", f)
		}
	}
}

func TestHelmGenerateFiles_Empty(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	helm := GenerateHelmFiles(result, "myapp")
	if helm != nil {
		t.Error("should return nil for empty project")
	}
}

func TestHelmChartYAML(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	chart := string(helm.Files["Chart.yaml"])
	checks := []string{
		"apiVersion: v2",
		"name: myapp",
		"type: application",
		"version: 0.1.0",
		"appVersion:",
	}
	for _, check := range checks {
		if !strings.Contains(chart, check) {
			t.Errorf("Chart.yaml missing %q", check)
		}
	}
}

func TestHelmValuesYAML_AllModulesListed(t *testing.T) {
	result := makeHelmResult("user", "post", "comment")
	helm := GenerateHelmFiles(result, "myapp")

	values := string(helm.Files["values.yaml"])
	checks := []string{
		"projectName: myapp",
		"gateway:",
		"modules:",
		"  user:",
		"  post:",
		"  comment:",
		"database:",
		"user: postgres",
		"password: postgres",
		"prefix: myapp",
		"redis:",
		"ingress:",
		"replicas:",
		"rpcPort: 9000",
		"cpu:",
		"memory:",
	}
	for _, check := range checks {
		if !strings.Contains(values, check) {
			t.Errorf("values.yaml missing %q", check)
		}
	}
}

func TestHelmValuesYAML_WithEvents(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/order.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "Order",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			Events: []*ast.EventDecl{{Name: "OrderCreated"}},
		}},
	}

	helm := GenerateHelmFiles(result, "shop")
	values := string(helm.Files["values.yaml"])

	if !strings.Contains(values, "nats:") {
		t.Error("values.yaml should include nats when events exist")
	}
	if !strings.Contains(values, "nats://nats:4222") {
		t.Error("values.yaml should have NATS URL")
	}
}

func TestHelmValuesYAML_NoEvents(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	values := string(helm.Files["values.yaml"])
	if strings.Contains(values, "nats:") {
		t.Error("values.yaml should NOT include nats when no events")
	}
}

func TestHelmGatewayDeployment(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	dep := string(helm.Files["templates/gateway-deployment.yaml"])
	checks := []string{
		"kind: Deployment",
		"component: gateway",
		"containerPort:",
		"livenessProbe:",
		"readinessProbe:",
		"/health",
		"image:",
		"gateway",
		"resources:",
		"envFrom:",
		"configMapRef:",
		"secretRef:",
	}
	for _, check := range checks {
		if !strings.Contains(dep, check) {
			t.Errorf("gateway-deployment.yaml missing %q", check)
		}
	}
}

func TestHelmGatewayService(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	svc := string(helm.Files["templates/gateway-service.yaml"])
	checks := []string{
		"kind: Service",
		"component: gateway",
		"targetPort: http",
		"name: http",
	}
	for _, check := range checks {
		if !strings.Contains(svc, check) {
			t.Errorf("gateway-service.yaml missing %q", check)
		}
	}
}

func TestHelmModuleDeployment(t *testing.T) {
	result := makeHelmResult("user", "post")
	helm := GenerateHelmFiles(result, "myapp")

	for _, mod := range []string{"user", "post"} {
		dep := string(helm.Files["templates/module-"+mod+"-deployment.yaml"])
		checks := []string{
			"kind: Deployment",
			"component: " + mod,
			"name: http",
			"name: rpc",
			"APP_PORT",
			"LUXO_PORT",
			"DEPLOY_MODE",
			"cluster",
			"livenessProbe:",
			"readinessProbe:",
			"/health",
			"resources:",
			"envFrom:",
		}
		for _, check := range checks {
			if !strings.Contains(dep, check) {
				t.Errorf("module-%s-deployment.yaml missing %q", mod, check)
			}
		}
	}
}

func TestHelmModuleService_Ports(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	svc := string(helm.Files["templates/module-user-service.yaml"])
	checks := []string{
		"kind: Service",
		"type: ClusterIP",
		"name: http",
		"name: rpc",
		".Values.modules.user.rpcPort",
		"targetPort: rpc",
		"component: user",
	}
	for _, check := range checks {
		if !strings.Contains(svc, check) {
			t.Errorf("module-user-service.yaml missing %q", check)
		}
	}
	// Ensure no hardcoded 9000
	if strings.Contains(svc, "port: 9000") {
		t.Error("module-user-service.yaml should not have hardcoded port: 9000")
	}
}

func TestHelmConfigMap_ServiceDiscovery(t *testing.T) {
	result := makeHelmResult("user", "post")
	helm := GenerateHelmFiles(result, "myapp")

	cm := string(helm.Files["templates/configmap.yaml"])
	checks := []string{
		"kind: ConfigMap",
		"DATABASE_DRIVER:",
		"DATABASE_HOST:",
		"DATABASE_PORT:",
		"DATABASE_PREFIX:",
		"REDIS_HOST:",
		"USER_SERVICE_ADDR:",
		"POST_SERVICE_ADDR:",
		".Values.modules.user.rpcPort",
		".Values.modules.post.rpcPort",
	}
	for _, check := range checks {
		if !strings.Contains(cm, check) {
			t.Errorf("configmap.yaml missing %q", check)
		}
	}
	// Ensure no hardcoded :9000
	if strings.Contains(cm, ":9000") {
		t.Error("configmap.yaml should not have hardcoded :9000")
	}
}

func TestHelmSecret(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	secret := string(helm.Files["templates/secret.yaml"])
	checks := []string{
		"kind: Secret",
		"type: Opaque",
		"DATABASE_USER:",
		"DATABASE_PASSWORD:",
		"b64enc",
	}
	for _, check := range checks {
		if !strings.Contains(secret, check) {
			t.Errorf("secret.yaml missing %q", check)
		}
	}
}

func TestHelmIngress(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	ingress := string(helm.Files["templates/ingress.yaml"])
	checks := []string{
		"kind: Ingress",
		"networking.k8s.io/v1",
		"ingress.enabled",
		"ingressClassName",
		"tls:",
		"rules:",
		"gateway",
	}
	for _, check := range checks {
		if !strings.Contains(ingress, check) {
			t.Errorf("ingress.yaml missing %q", check)
		}
	}
}

func TestHelmHPA(t *testing.T) {
	result := makeHelmResult("user", "post")
	helm := GenerateHelmFiles(result, "myapp")

	hpa := string(helm.Files["templates/hpa.yaml"])
	checks := []string{
		"kind: HorizontalPodAutoscaler",
		"autoscaling/v2",
		"gateway",
		"user",
		"post",
		"targetCPUUtilizationPercentage",
	}
	for _, check := range checks {
		if !strings.Contains(hpa, check) {
			t.Errorf("hpa.yaml missing %q", check)
		}
	}
}

func TestHelmServiceAccount(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	sa := string(helm.Files["templates/serviceaccount.yaml"])
	checks := []string{
		"kind: ServiceAccount",
		"serviceAccount.create",
	}
	for _, check := range checks {
		if !strings.Contains(sa, check) {
			t.Errorf("serviceaccount.yaml missing %q", check)
		}
	}
}

func TestHelmNormalizeChartName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"myapp", "myapp"},
		{"MyApp", "myapp"},
		{"My_App", "my-app"},
		{"my.app.io", "my-app-io"},
		{"MY APP", "my-app"},
		{"---test---", "test"},
		{"", "luxo-app"},
		{"___", "luxo-app"},
		{strings.Repeat("a", 60), strings.Repeat("a", 53)},
		{"Hello World!", "hello-world"},
		{"app@v2.0", "app-v2-0"},
	}
	for _, tt := range tests {
		got := normalizeChartName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeChartName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHelmNormalizeChartNameApplied(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "My_App")
	chart := string(helm.Files["Chart.yaml"])
	if !strings.Contains(chart, "name: my-app") {
		t.Errorf("Chart.yaml should use normalized name, got:\n%s", chart)
	}
}

func TestHelmHelpers(t *testing.T) {
	result := makeHelmResult("user")
	helm := GenerateHelmFiles(result, "myapp")

	helpers := string(helm.Files["templates/_helpers.tpl"])
	checks := []string{
		"luxo.name",
		"luxo.fullname",
		"luxo.chart",
		"luxo.labels",
		"luxo.selectorLabels",
		"luxo.serviceAccountName",
	}
	for _, check := range checks {
		if !strings.Contains(helpers, check) {
			t.Errorf("_helpers.tpl missing %q", check)
		}
	}
}

func TestHelmConfigMap_WithEvents(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/order.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "Order",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			Events: []*ast.EventDecl{{Name: "OrderCreated"}},
		}},
	}

	helm := GenerateHelmFiles(result, "shop")
	cm := string(helm.Files["templates/configmap.yaml"])

	if !strings.Contains(cm, "NATS_URL:") {
		t.Error("configmap should include NATS_URL when events exist")
	}
}

func TestHelmFileCount(t *testing.T) {
	result := makeHelmResult("user", "post", "comment")
	helm := GenerateHelmFiles(result, "myapp")

	// Base files: Chart.yaml, values.yaml, .helmignore = 3
	// Template files: _helpers.tpl, configmap, secret, serviceaccount,
	//   gateway-deployment, gateway-service, ingress, hpa = 8
	// Per-module: 3 modules * 2 (deployment + service) = 6
	// Total: 3 + 8 + 6 = 17
	expected := 17
	if len(helm.Files) != expected {
		t.Errorf("expected %d files, got %d", expected, len(helm.Files))
	}
}
