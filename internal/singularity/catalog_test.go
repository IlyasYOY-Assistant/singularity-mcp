package singularity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/IlyasYOY/singularity-mcp/openapi"
)

func TestCatalogCoverage(t *testing.T) {
	catalog, err := NewCatalog(openapi.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.TotalOperations != 51 {
		t.Fatalf("total ops = %d", catalog.TotalOperations)
	}
	if catalog.ExposedOperationCount() != 48 {
		t.Fatalf("exposed ops = %d", catalog.ExposedOperationCount())
	}
	if len(catalog.Groups) != 8 {
		t.Fatalf("groups = %d", len(catalog.Groups))
	}
	for _, name := range []string{"kanban-status", "kanban-task-status"} {
		found := false
		for _, omitted := range catalog.OmittedTags {
			found = found || omitted == name
		}
		if !found {
			t.Fatalf("omitted tags missing %s: %v", name, catalog.OmittedTags)
		}
	}
	if _, ok := catalog.Group("singularity_kanban_statuses"); ok {
		t.Fatal("kanban group exposed")
	}
	for _, tool := range []string{"singularity_tasks", "singularity_projects", "singularity_tags"} {
		search, ok := catalog.Operation(tool, "search")
		if !ok {
			t.Fatalf("%s search op missing", tool)
		}
		list, _ := catalog.Operation(tool, "list")
		if search.Method != list.Method || search.Path != list.Path || search.ListResponseField != list.ListResponseField {
			t.Fatalf("%s search = %#v, list = %#v", tool, search, list)
		}
	}
}

func TestCatalogOperationDetails(t *testing.T) {
	catalog, err := NewCatalog(openapi.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := catalog.Operation("singularity_tasks", "list")
	if !ok {
		t.Fatal("task list op missing")
	}
	if op.Method != "GET" || op.Path != "/v2/task" {
		t.Fatalf("task list = %s %s", op.Method, op.Path)
	}
	if op.ListResponseField != "tasks" {
		t.Fatalf("list field = %q", op.ListResponseField)
	}
	for _, name := range []string{"inbox", "overdue", "today", "only-today"} {
		taskOp, ok := catalog.Operation("singularity_tasks", name)
		if !ok {
			t.Fatalf("task %s op missing", name)
		}
		if taskOp.Method != op.Method || taskOp.Path != op.Path || taskOp.ListResponseField != op.ListResponseField {
			t.Fatalf("task %s = %#v", name, taskOp)
		}
	}

	create, ok := catalog.Operation("singularity_habit_progress", "create")
	if !ok {
		t.Fatal("habit progress create op missing")
	}
	want := []string{"habit", "date", "progress"}
	if len(create.BodyRequired) != len(want) {
		t.Fatalf("required = %v", create.BodyRequired)
	}
	for i := range want {
		if create.BodyRequired[i] != want[i] {
			t.Fatalf("required = %v", create.BodyRequired)
		}
	}
}

func TestResolveSchemaRecursivelyAndRejectsCycles(t *testing.T) {
	doc := openAPIDoc{}
	doc.Components.Schemas = map[string]map[string]any{
		"Outer": {"type": "object", "properties": map[string]any{"inner": map[string]any{"$ref": "#/components/schemas/Inner"}}},
		"Inner": {"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}
	resolved, err := resolveSchemaDeep(doc, map[string]any{"$ref": "#/components/schemas/Outer"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resolved)
	if strings.Contains(string(raw), `"$ref"`) || !strings.Contains(string(raw), `"value"`) {
		t.Fatalf("resolved = %s", raw)
	}

	doc.Components.Schemas["Inner"] = map[string]any{"$ref": "#/components/schemas/Outer"}
	if _, err := resolveSchemaDeep(doc, map[string]any{"$ref": "#/components/schemas/Outer"}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestResolveSchemaRejectsUnknownRefsAndPreservesSiblings(t *testing.T) {
	doc := openAPIDoc{}
	doc.Components.Schemas = map[string]map[string]any{
		"Known": {"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}
	if _, err := resolveSchemaDeep(doc, map[string]any{"$ref": "#/components/schemas/Missing"}); err == nil || !strings.Contains(err.Error(), "unknown schema ref") {
		t.Fatalf("unknown-ref error = %v", err)
	}
	resolved, err := resolveSchemaDeep(doc, map[string]any{
		"$ref":        "#/components/schemas/Known",
		"description": "call-site description",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["description"] != "call-site description" || resolved["properties"] == nil {
		t.Fatalf("resolved siblings = %v", resolved)
	}
}

func TestCatalogProjectBodiesContainNoRefs(t *testing.T) {
	catalog, err := NewCatalog(openapi.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"create", "update"} {
		op, _ := catalog.Operation("singularity_projects", name)
		raw, _ := json.Marshal(op.BodySchema)
		if strings.Contains(string(raw), `"$ref"`) {
			t.Fatalf("%s body has ref: %s", name, raw)
		}
		if !strings.Contains(string(raw), "reviewValidationInterval") {
			t.Fatalf("%s body missing interval: %s", name, raw)
		}
	}
}
