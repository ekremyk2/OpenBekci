// Package deploytest checks the deployment artifacts that cannot be built here.
package deploytest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const chart = "../../deploy/helm/openbekci"

func load(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(chart, name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.UnmarshalStrict(b, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

// Parse only: `helm lint` and `helm template` are the real checks (CI runs them).
func TestChartValuesParse(t *testing.T) {
	c := load(t, "Chart.yaml")
	for _, k := range []string{"name", "version", "appVersion", "dependencies"} {
		if c[k] == nil {
			t.Errorf("Chart.yaml: %s missing", k)
		}
	}
	v := load(t, "values.yaml")
	if as, _ := v["server"].(map[string]any)["autoscaling"].(map[string]any); as["enabled"] != false {
		t.Error("autoscaling must be off by default")
	}
	keys, _ := v["secrets"].(map[string]any)["keys"].(map[string]any)
	for _, k := range []string{"DATABASE_URL", "MIGRATOR_DATABASE_URL", "PLATFORM_DATABASE_URL", "SECRET_KEY", "ENCRYPTION_KEY", "METRICS_TOKEN"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("values.yaml: secrets.keys.%s missing", k)
		}
	}
	if len(v["otel"].(map[string]any)) != 0 {
		t.Error("tracing must be off by default")
	}
}

func TestChartProbes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(chart, "templates", "server.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"path: /readyz", "path: /healthz"} {
		if !strings.Contains(string(b), p) {
			t.Errorf("server.yaml lacks %s", p)
		}
	}
}
