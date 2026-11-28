package codegen

import (
	"bytes"
	"maps"
	"slices"
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

func TestHelmCheckedBackendSelection(t *testing.T) {
	result := makeHelmResult("user")
	result.Files[0].APIs = []*ast.ApiDecl{{Name: "watch", Directives: []*ast.Directive{{Name: "stream"}}}}
	result.Files = append(result.Files, &ast.File{Name: "origin/read.luxo", APIs: []*ast.ApiDecl{{Name: "readValue", Directives: []*ast.Directive{{Name: "cache"}}}}})
	for _, sample := range []struct {
		selection                     EntryBackends
		gatewayNATS, watchNATS, redis bool
	}{
		{EntryBackends{Events: "memory", Cache: "memory"}, false, false, false},
		{EntryBackends{Events: "memory", Queue: "nats", Cache: "memory"}, false, true, false},
		{EntryBackends{Events: "nats", Cache: "redis"}, true, true, true},
	} {
		chart, err := GenerateHelmFilesChecked(result, "audit", sample.selection)
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range []struct {
			file, env string
			want      bool
		}{
			{"gateway", "NATS_URL", sample.gatewayNATS},
			{"module-user", "NATS_URL", sample.watchNATS},
			{"module-user", "LUXO_CACHE_PROJECT", false},
			{"module-read", "REDIS_URL", sample.redis},
			{"module-read", "LUXO_CACHE_PROJECT", true},
		} {
			source := chart.Files["templates/"+check.file+"-deployment.yaml"]
			if strings.Contains(string(source), "name: "+check.env+"\n") != check.want {
				t.Errorf("selection %+v: %s requires %s = %v", sample.selection, check.file, check.env, check.want)
			}
		}
	}
}

func TestHelmCheckedRejectsInvalidInput(t *testing.T) {
	invalid := analyzePermissionFixture(t, "type Detail { level: Int }\nmodel Record { id: Int @id detail: Detail }")
	for _, sample := range []struct {
		result     *semantic.Result
		options    EntryBackends
		diagnostic string
	}{
		{nil, EntryBackends{}, "requires a semantic result"},
		{invalid, EntryBackends{}, "has no database mapping"},
		{makeHelmResult("user"), EntryBackends{Queue: "unsupported"}, "unsupported queue backend"},
	} {
		chart, err := GenerateHelmFilesChecked(sample.result, "audit", sample.options)
		if chart != nil || err == nil || !strings.Contains(err.Error(), sample.diagnostic) {
			t.Fatalf("invalid result: generated=%v error=%v", chart != nil, err)
		}
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
		`include "luxo.environmentSources" . | nindent 12`,
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
			"component: " + strconv.Quote(mod),
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
			`include "luxo.environmentSources" . | nindent 12`,
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
		`component: "user"`,
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

func TestHelmDeploymentsAnnotateConfigurationRollout(t *testing.T) {
	result := makeHelmResult("user", "post")
	result.Files[0].Events = []*ast.EventDecl{{Name: "UserChanged"}}
	chart := GenerateHelmFiles(result, "myapp")
	for _, name := range []string{"gateway", "module-user", "module-post"} {
		_, pod, ok := strings.Cut(string(chart.Files["templates/"+name+"-deployment.yaml"]), "\n  template:\n")
		if !ok {
			t.Fatalf("%s has no Pod template", name)
		}
		metadata, _, ok := strings.Cut(pod, "\n    spec:\n")
		if !ok || !strings.Contains(metadata, `include "luxo.chartRolloutAnnotations" . | nindent 6`) {
			t.Errorf("%s does not include rollout annotations in Pod metadata", name)
		}
	}
	var values struct {
		Secrets struct {
			Revision *string `yaml:"revision"`
		} `yaml:"secrets"`
	}
	if err := yaml.Unmarshal(chart.Files["values.yaml"], &values); err != nil {
		t.Fatal(err)
	}
	if values.Secrets.Revision == nil || *values.Secrets.Revision != "" {
		t.Fatal("secret revision must be documented and optional")
	}
	helpers := string(chart.Files["templates/_helpers.tpl"])
	_, rollout, defined := strings.Cut(helpers, `{{- define "luxo.chartRolloutAnnotations" -}}`)
	rollout, envSources, helperEnd := strings.Cut(rollout, `{{- define "luxo.environmentSources" -}}`)
	if !defined || !helperEnd || !strings.Contains(rollout, `checksum/config: {{ include (print .Template.BasePath "/_helpers.tpl") . | trim | sha256sum | quote }}`) ||
		!strings.Contains(rollout, `luxo/secret-revision: {{ . | quote }}`) || !strings.Contains(rollout, `{{- with .Values.secrets }}`) ||
		!strings.Contains(rollout, `{{- with .revision }}`) {
		t.Fatal("rollout must use public configuration checksum and optional Secret revision")
	}
	envSources, _, helperEnd = strings.Cut(envSources, "{{- end }}")
	if !helperEnd || !strings.Contains(envSources, "configMapRef:") || !strings.Contains(envSources, "secretRef:") {
		t.Fatal("deployment containers must share the ConfigMap and Secret envFrom references")
	}
	public := helmPublicConfigurationBody(t, helpers)
	if !strings.Contains(public, "DATABASE_HOST:") || strings.Contains(public, "NATS_URL") ||
		strings.Contains(public, ".Values.database.user") || strings.Contains(public, ".Values.database.password") {
		t.Fatal("the shared checksum input must contain public settings and exclude credentials")
	}
	configMap := string(chart.Files["templates/configmap.yaml"])
	if !strings.Contains(configMap, `include (print .Template.BasePath "/_helpers.tpl") . | trim | nindent 2`) || !strings.Contains(configMap, "NATS_URL:") {
		t.Fatal("legacy ConfigMap must retain its URL outside the shared public configuration")
	}
}

func helmPublicConfigurationBody(t *testing.T, helpers string) string {
	t.Helper()
	_, public, found := strings.Cut(helpers, "\nDATABASE_DRIVER:")
	public, _, end := strings.Cut(public, `{{- define "luxo.chartRolloutAnnotations" -}}`)
	if !found || !end {
		t.Fatal("the chart-local public configuration must precede the shared rollout helper")
	}
	return "DATABASE_DRIVER:" + public
}

func TestHelmPublicConfigurationKeepsChartAndLegacyHelpersIndependent(t *testing.T) {
	for _, module := range []string{"user", "post"} {
		chart := GenerateHelmFiles(makeHelmResult(module), module)
		helpers := string(chart.Files["templates/_helpers.tpl"])
		if strings.Contains(helpers, `define "luxo.publicConfiguration"`) ||
			strings.Contains(helpers, `define "luxo.rolloutAnnotations"`) {
			t.Fatal("a new chart can override an older chart's shared public templates")
		}
		data := helmPublicConfigurationBody(t, helpers)
		if !strings.Contains(data, strings.ToUpper(module)+"_SERVICE_ADDR:") || strings.Count(data, "_SERVICE_ADDR:") != 1 {
			t.Fatalf("public configuration must have only this chart's service address: %s", data)
		}
	}
}

func TestHelmHelpersSizeMatchesTemplates(t *testing.T) {
	for _, modules := range [][]moduleInfo{
		{{name: "module001"}},
		{{name: "user"}, {name: "dotless-ı"}, {name: "latin-ɜ"}},
		{{name: "gateway"}, {name: "module-gateway"}},
	} {
		names := planDeploymentAliases(modules, "", "gateway")
		got := len(generateHelpers(modules, names))
		if predicted := helmHelpersSize(modules, names); predicted != got {
			t.Errorf("helpers size = %d, want %d emitted bytes", predicted, got)
		}
	}
}

func TestHelmGatewayModuleHasDistinctDeploymentIdentity(t *testing.T) {
	chart, err := GenerateHelmFilesChecked(makeHelmResult("gateway", "postgres", "builder"), "probe", EntryBackends{})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string][]string{
		"templates/gateway-deployment.yaml":        {`name: {{ include "luxo.resourceName" (list . "gateway") }}`, `/gateway:{{ .Values.image.tag }}`},
		"templates/module-gateway-deployment.yaml": {`name: {{ include "luxo.resourceName" (list . "module-gateway") }}`, "app.kubernetes.io/component: \"module-gateway\"", `/module-gateway:{{ .Values.image.tag }}`, ".Values.modules.gateway.replicas"},
		"templates/module-gateway-service.yaml":    {`name: {{ include "luxo.resourceName" (list . "module-gateway") }}`, "app.kubernetes.io/component: \"module-gateway\"", ".Values.modules.gateway.rpcPort"},
		"templates/module-postgres-service.yaml":   {`name: {{ include "luxo.resourceName" (list . "postgres") }}`},
		"templates/module-builder-service.yaml":    {`name: {{ include "luxo.resourceName" (list . "builder") }}`},
		"templates/hpa.yaml":                       {`name: {{ include "luxo.resourceName" (list . "module-gateway") }}`, ".Values.modules.gateway.autoscaling.enabled"},
		"templates/_helpers.tpl":                   {`GATEWAY_SERVICE_ADDR: {{ include "luxo.resourceName" (list . "module-gateway") }}:{{ .Values.modules.gateway.rpcPort }}`},
		"values.yaml":                              {"modules:\n  gateway:\n"},
	}
	for path, expected := range checks {
		for _, pattern := range expected {
			if !strings.Contains(string(chart.Files[path]), pattern) {
				t.Errorf("%s is missing %q", path, pattern)
			}
		}
	}
	if got := strings.Count(string(chart.Files["templates/hpa.yaml"]), `name: {{ include "luxo.resourceName" (list . "module-gateway") }}`); got != 2 {
		t.Errorf("module HPA must have a distinct name and target, got %d references", got)
	}
}

func TestHelmModuleNamesPreserveExternalReferences(t *testing.T) {
	chart, err := GenerateHelmFilesChecked(makeHelmResult("order_items", "order__items", "gateway", "user"), "demo", EntryBackends{Queue: "nats"})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string][]string{
		"templates/module-order_items-deployment.yaml": {`name: {{ include "luxo.resourceName" (list . "order-items") }}`, "app.kubernetes.io/component: \"order-items\"\n", "/order_items:{{ .Values.image.tag }}", `- name: "order-items"`, ".Values.modules.order_items.replicas", "- name: NATS_URL"},
		"templates/module-order_items-service.yaml":    {`name: {{ include "luxo.resourceName" (list . "order-items") }}`, "app.kubernetes.io/component: \"order-items\"\n", ".Values.modules.order_items.rpcPort"},
		"templates/module-order__items-service.yaml":   {`name: {{ include "luxo.resourceName" (list . "order-items-2") }}`, "app.kubernetes.io/component: \"order-items-2\"\n", ".Values.modules.order__items.rpcPort"},
		"templates/module-gateway-deployment.yaml":     {`name: {{ include "luxo.resourceName" (list . "module-gateway") }}`, "/module-gateway:{{ .Values.image.tag }}"},
		"templates/module-user-service.yaml":           {`name: {{ include "luxo.resourceName" (list . "user") }}`},
		"templates/_helpers.tpl":                       {`ORDER_ITEMS_SERVICE_ADDR: {{ include "luxo.resourceName" (list . "order-items") }}:{{ .Values.modules.order_items.rpcPort }}`, `ORDER__ITEMS_SERVICE_ADDR: {{ include "luxo.resourceName" (list . "order-items-2") }}:{{ .Values.modules.order__items.rpcPort }}`},
		"templates/hpa.yaml":                           {`name: {{ include "luxo.resourceName" (list . "order-items") }}`, ".Values.modules.order_items.autoscaling.enabled"},
		"templates/configmap.yaml":                     {`list "gateway" "order-items" "order-items-2" "module-gateway" "user"`, `if hasKey $seen $name`, `fail (printf`},
		"values.yaml":                                  {"  order_items:\n", "  order__items:\n"},
	}
	for path, patterns := range checks {
		for _, pattern := range patterns {
			if !bytes.Contains(chart.Files[path], []byte(pattern)) {
				t.Errorf("%s does not contain %q", path, pattern)
			}
		}
	}
}

func TestHelmModuleLabelBoundsAndStability(t *testing.T) {
	long := strings.Repeat("long", 17)
	modules := []moduleInfo{{name: "order_items"}, {name: "order__items"}, {name: "order-items"},
		{name: long + "a"}, {name: long + "b"}, {name: "_Gateway"}, {name: "_1"}, {name: "_"},
		{name: strings.Repeat("x", 63)}}
	names := planHelmResourceNames(modules, nil)
	if names.name("order-items") != "order-items" || names.name("order_items") != "order-items-2" ||
		names.name("order__items") != "order-items-3" || names.name("_Gateway") != "gateway-2" ||
		names.name("_1") != "1" || names.name("_") != "module" {
		t.Fatalf("unexpected Helm resource assignments: %v", names)
	}
	claimed := map[string]bool{"gateway": true}
	for _, module := range modules {
		label := names.name(module.name)
		if claimed[label] || !isHelmModuleLabel(label) {
			t.Fatalf("duplicate or invalid resource label %q for %q", label, module.name)
		}
		claimed[label] = true
	}
	if valid := strings.Repeat("x", 63); names.name(valid) != valid {
		t.Fatalf("already valid component changed: %q", names.name(valid))
	}
	slices.Reverse(modules)
	if reverse := planHelmResourceNames(modules, nil); !maps.Equal(names, reverse) {
		t.Fatalf("resource names depend on module traversal: %v != %v", names, reverse)
	}
}

func TestHelmConfigMap_ServiceDiscovery(t *testing.T) {
	result := makeHelmResult("user", "post")
	helm := GenerateHelmFiles(result, "myapp")

	cm := string(helm.Files["templates/configmap.yaml"])
	if !strings.Contains(cm, "kind: ConfigMap") || !strings.Contains(cm, `include (print .Template.BasePath "/_helpers.tpl") . | trim | nindent 2`) {
		t.Fatal("ConfigMap must consume the shared configuration")
	}
	data := helmPublicConfigurationBody(t, string(helm.Files["templates/_helpers.tpl"]))
	checks := []string{
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
		if !strings.Contains(data, check) {
			t.Errorf("configmap.yaml missing %q", check)
		}
	}
	// Ensure no hardcoded :9000
	if strings.Contains(data, ":9000") {
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

func TestGenerateHPAOutputAllocation(t *testing.T) {
	for _, count := range []int{0, 1, 16} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			modules := make([]moduleInfo, count)
			for index := range modules {
				modules[index].name = "module" + strconv.Itoa(index)
			}
			var output []byte
			allocations := testing.AllocsPerRun(20, func() { output = generateHPA(modules, nil) })
			if allocations != 1 {
				t.Fatalf("HPA output needs one owned byte allocation, got %g", allocations)
			}
			if len(output) != cap(output) {
				t.Fatalf("HPA output capacity %d != size %d", cap(output), len(output))
			}
		})
	}
}

func TestGenerateHPAOutputOwnership(t *testing.T) {
	for _, name := range []string{"user", "order_items", strings.Repeat("long", 22), "用户"} {
		modules := []moduleInfo{{name: name}}
		names := planHelmResourceNames(modules, nil)
		first, second := generateHPA(modules, names), generateHPA(modules, names)
		original := bytes.Clone(second)
		first[0] ^= 1
		if !bytes.Equal(second, original) {
			t.Fatal("HPA outputs share mutable storage")
		}
		if len(second) != cap(second) {
			t.Fatalf("normalized HPA output capacity %d != size %d", cap(second), len(second))
		}
		if !bytes.Contains(second, []byte(".Values.modules."+name+".autoscaling.enabled")) {
			t.Fatal("values lookup spelling changed")
		}
		if !bytes.Contains(second, []byte(names.name(name))) {
			t.Fatal("resource name disappeared")
		}
	}
}
