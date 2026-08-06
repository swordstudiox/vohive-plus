package api

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadOpenAPIDoc(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("openapi.vohive.yaml")
	if err != nil {
		t.Fatalf("read openapi.vohive.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("openapi.vohive.yaml is invalid YAML: %v", err)
	}
	return doc
}

func TestOpenAPIVoHiveYAMLValid(t *testing.T) {
	doc := loadOpenAPIDoc(t)
	if doc["openapi"] == "" {
		t.Fatalf("openapi.vohive.yaml missing openapi version")
	}
}

func TestOpenAPIRoamingPatchDocumentsDataRoamingResponse(t *testing.T) {
	doc := loadOpenAPIDoc(t)

	paths := doc["paths"].(map[string]any)
	roamingPath := paths["/devices/{device_id}/roaming"].(map[string]any)
	patch := roamingPath["patch"].(map[string]any)
	summary := patch["summary"].(string)
	if !strings.Contains(summary, "数据漫游") {
		t.Fatalf("roaming summary=%q want 数据漫游语义", summary)
	}

	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	roamingResp := schemas["RoamingActionResponse"].(map[string]any)
	props := roamingResp["properties"].(map[string]any)
	for _, field := range []string{"status", "message", "roaming_enabled"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("RoamingActionResponse missing %s: %+v", field, props)
		}
	}
	if _, ok := props["response"]; ok {
		t.Fatalf("RoamingActionResponse must not expose legacy response field: %+v", props)
	}
}

func TestOpenAPIDeviceConfigDTODoesNotExposeRoamingPolicy(t *testing.T) {
	doc := loadOpenAPIDoc(t)

	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	deviceConfig := schemas["DeviceConfigDTO"].(map[string]any)
	props := deviceConfig["properties"].(map[string]any)
	if _, ok := props["roaming_enabled"]; ok {
		t.Fatalf("DeviceConfigDTO must not expose roaming_enabled because data roaming is card policy state: %+v", props["roaming_enabled"])
	}
}
