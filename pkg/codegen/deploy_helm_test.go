package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

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
		"port: 9000",
		"targetPort: rpc",
		"component: user",
	}
	for _, check := range checks {
		if !strings.Contains(svc, check) {
			t.Errorf("module-user-service.yaml missing %q", check)
		}
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
	}
	for _, check := range checks {
		if !strings.Contains(cm, check) {
			t.Errorf("configmap.yaml missing %q", check)
		}
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
