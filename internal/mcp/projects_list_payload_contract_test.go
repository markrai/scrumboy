package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func callProjectsListRaw(t *testing.T, client *http.Client, url string, payload map[string]any, jsonRPC bool) []byte {
	t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal projects_list request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("new projects_list request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if jsonRPC {
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("call projects_list: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read projects_list response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("projects_list status=%d body=%s", resp.StatusCode, raw)
	}
	return raw
}

func assertImageFreeProjectSummary(t *testing.T, item map[string]any, project store.Project) {
	t.Helper()

	wantKeys := map[string]bool{
		"projectSlug":        true,
		"projectId":          true,
		"name":               true,
		"dominantColor":      true,
		"defaultSprintWeeks": true,
		"expiresAt":          true,
		"createdAt":          true,
		"updatedAt":          true,
		"role":               true,
	}
	if len(item) != len(wantKeys) {
		t.Fatalf("project summary keys=%v, want exactly %v", item, wantKeys)
	}
	for key := range wantKeys {
		if _, exists := item[key]; !exists {
			t.Fatalf("project summary missing %q: %#v", key, item)
		}
	}
	if _, exists := item["image"]; exists {
		t.Fatalf("project summary contains image: %#v", item)
	}
	if item["projectSlug"] != project.Slug || item["projectId"] != float64(project.ID) || item["name"] != project.Name {
		t.Fatalf("project summary identity=%#v, want slug=%q id=%d name=%q", item, project.Slug, project.ID, project.Name)
	}
	if item["dominantColor"] != project.DominantColor || item["defaultSprintWeeks"] != float64(project.DefaultSprintWeeks) || item["role"] != "maintainer" {
		t.Fatalf("project summary fields=%#v, want source project values and maintainer role", item)
	}
}

func assertProjectsListWireResult(t *testing.T, raw []byte, project store.Project, jsonRPC bool) {
	t.Helper()

	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode projects_list response: %v body=%s", err, raw)
	}

	var structured map[string]any
	if jsonRPC {
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("JSON-RPC result=%#v, want object", response["result"])
		}
		structured, ok = result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatalf("structuredContent=%#v, want object", result["structuredContent"])
		}
		content, ok := result["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("content=%#v, want one text block", result["content"])
		}
		block := content[0].(map[string]any)
		var textValue map[string]any
		if err := json.Unmarshal([]byte(block["text"].(string)), &textValue); err != nil {
			t.Fatalf("decode content text: %v", err)
		}
		if !reflect.DeepEqual(textValue, structured) {
			t.Fatalf("content text=%#v, want structuredContent=%#v", textValue, structured)
		}
	} else {
		if response["ok"] != true {
			t.Fatalf("legacy response=%#v, want ok=true", response)
		}
		structured = response["data"].(map[string]any)
		meta := response["meta"].(map[string]any)
		if len(meta) != 2 || meta["nextCursor"] != nil || meta["hasMore"] != false {
			t.Fatalf("legacy pagination meta=%#v, want final page", meta)
		}
	}

	items, ok := structured["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("projects_list items=%#v, want one project", structured["items"])
	}
	assertImageFreeProjectSummary(t, items[0].(map[string]any), project)
	if jsonRPC {
		if len(structured) != 3 || structured["nextCursor"] != nil || structured["hasMore"] != false {
			t.Fatalf("JSON-RPC structured result=%#v, want items plus final-page metadata", structured)
		}
	}
}

func TestProjectsListPayloadIsIndependentOfStoredImageAcrossTransports(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := store.New(sqlDB, nil).CreateProject(ctx, "Payload Contract Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE projects SET image = NULL WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("clear baseline image: %v", err)
	}

	legacyRequest := map[string]any{"tool": "projects_list", "input": map[string]any{"limit": 20}}
	rpcRequest := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects_list",
			"arguments": map[string]any{"limit": 20},
		},
	}
	baselineLegacy := callProjectsListRaw(t, client, ts.URL+"/mcp", legacyRequest, false)
	baselineRPC := callProjectsListRaw(t, client, ts.URL+"/mcp/rpc", rpcRequest, true)
	assertProjectsListWireResult(t, baselineLegacy, project, false)
	assertProjectsListWireResult(t, baselineRPC, project, true)

	const sentinel = "MCP_PROJECT_IMAGE_SENTINEL"
	largeImage := "data:image/png;base64," + sentinel + strings.Repeat("A", 256*1024)
	if _, err := sqlDB.Exec(`UPDATE projects SET image = ? WHERE id = ?`, largeImage, project.ID); err != nil {
		t.Fatalf("seed large project image: %v", err)
	}
	largeLegacy := callProjectsListRaw(t, client, ts.URL+"/mcp", legacyRequest, false)
	largeRPC := callProjectsListRaw(t, client, ts.URL+"/mcp/rpc", rpcRequest, true)

	for name, raw := range map[string][]byte{"legacy": largeLegacy, "json-rpc": largeRPC} {
		t.Run(name, func(t *testing.T) {
			lower := strings.ToLower(string(raw))
			if strings.Contains(lower, `"image"`) || strings.Contains(lower, "data:image") || strings.Contains(string(raw), sentinel) {
				t.Fatalf("projects_list leaked stored image data: response bytes=%d", len(raw))
			}
			assertProjectsListWireResult(t, raw, project, name == "json-rpc")
		})
	}
	if len(largeLegacy) != len(baselineLegacy) {
		t.Fatalf("legacy response grew with image: baseline=%d large-image=%d", len(baselineLegacy), len(largeLegacy))
	}
	if len(largeRPC) != len(baselineRPC) {
		t.Fatalf("JSON-RPC response grew with image: baseline=%d large-image=%d", len(baselineRPC), len(largeRPC))
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/projects", nil)
	if err != nil {
		t.Fatalf("new REST project request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get REST projects: %v", err)
	}
	defer resp.Body.Close()
	restBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read REST projects: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Contains(restBody, []byte(sentinel)) {
		t.Fatalf("REST projects did not preserve image: status=%d bytes=%d", resp.StatusCode, len(restBody))
	}
}
