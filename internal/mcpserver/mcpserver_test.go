package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/charltonadam/tilefall/assets"
	"github.com/charltonadam/tilefall/internal/game"
	"github.com/charltonadam/tilefall/internal/scene"
	"github.com/charltonadam/tilefall/internal/store"
)

func connect(t *testing.T) (*mcp.ClientSession, *game.Game) {
	t.Helper()
	cat, err := assets.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g, err := game.Open(cat, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, svt := mcp.NewInMemoryTransports()
	if _, err := NewServer(g).Connect(ctx, svt, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, g
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, text(res))
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsBuildAMap(t *testing.T) {
	cs, g := connect(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 20 {
		t.Fatalf("only %d tools registered", len(tools.Tools))
	}

	call(t, cs, "new_scene", map[string]any{"name": "Forest ford", "width": 24, "height": 16, "gridType": "hex", "terrain": "grass"})
	call(t, cs, "apply_batch", map[string]any{"commands": []any{
		map[string]any{"type": "fill_terrain", "region": map[string]any{"rect": map[string]any{"from": map[string]any{"col": 0, "row": 0}, "to": map[string]any{"col": 8, "row": 15}}}, "terrain": "forest_floor"},
		map[string]any{"type": "paint_path", "points": []any{map[string]any{"col": 12, "row": 0}, map[string]any{"col": 14, "row": 8}, map[string]any{"col": 11, "row": 15}}, "width": 3, "terrain": "water_deep"},
		map[string]any{"type": "scatter_objects", "region": map[string]any{"all": true}, "assetIds": []any{"tree_oak", "tree_pine"}, "density": 0.3, "seed": 7, "minSpacing": 1.2, "onTerrain": []any{"forest_floor"}},
		map[string]any{"type": "add_token", "name": "Ranger", "cell": map[string]any{"col": 18, "row": 8}},
	}})

	// errors come back as tool errors the agent can read, not protocol errors
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "fill_terrain", Arguments: map[string]any{"region": map[string]any{"all": true}, "terrain": "chocolate"}})
	if err != nil || !res.IsError || !strings.Contains(text(res), "unknown terrain") {
		t.Fatalf("want tool error for bad terrain, got err=%v res=%v", err, res)
	}

	sum := text(call(t, cs, "get_scene_summary", nil))
	for _, want := range []string{"Forest ford", "forest_floor", "water_deep", "Ranger"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary missing %q:\n%s", want, sum)
		}
	}

	var ranger string
	g.Table.Read(func(s *scene.Scene) {
		for id := range s.Tokens {
			ranger = id
		}
	})
	m := text(call(t, cs, "measure", map[string]any{"from": map[string]any{"token": ranger}, "to": map[string]any{"cell": map[string]any{"col": 18, "row": 4}}}))
	if !strings.Contains(m, `"distance": 20`) || !strings.Contains(m, `"units": "ft"`) {
		t.Errorf("measure 4 hexes = %s, want 20 ft", m)
	}

	img := call(t, cs, "render_preview", nil)
	ic, ok := img.Content[0].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("render_preview returned %T", img.Content[0])
	}
	if _, err := png.Decode(bytes.NewReader(ic.Data)); err != nil {
		t.Fatalf("preview is not a PNG: %v", err)
	}
	if p := os.Getenv("TILEFALL_PREVIEW_OUT"); p != "" {
		os.WriteFile(p, ic.Data, 0o644)
	}

	call(t, cs, "undo", nil)
	var tokens int
	g.Table.Read(func(s *scene.Scene) { tokens = len(s.Tokens) })
	if tokens != 0 {
		t.Fatal("undo did not revert the batch")
	}
}

func TestTokenLibraryTools(t *testing.T) {
	cs, g := connect(t)
	out := text(call(t, cs, "create_token_template", map[string]any{"name": "Gnoll", "color": "#a07040", "maxHp": 22}))
	var tpl struct{ ID string }
	if err := json.Unmarshal([]byte(out), &tpl); err != nil || tpl.ID == "" {
		t.Fatalf("create_token_template = %s", out)
	}
	if list := text(call(t, cs, "list_token_templates", nil)); !strings.Contains(list, "Gnoll") {
		t.Fatalf("library missing Gnoll:\n%s", list)
	}
	// templates resolve inside apply_batch too
	call(t, cs, "apply_batch", map[string]any{"commands": []any{
		map[string]any{"type": "add_token", "templateId": tpl.ID, "cell": map[string]any{"col": 1, "row": 1}},
		map[string]any{"type": "add_token", "templateId": tpl.ID, "cell": map[string]any{"col": 2, "row": 1}},
	}})
	call(t, cs, "update_token_template", map[string]any{"id": tpl.ID, "maxHp": 30})
	g.Table.Read(func(s *scene.Scene) {
		names := map[string]int{}
		for _, tok := range s.Tokens {
			names[tok.Name] = tok.HP
		}
		if names["Gnoll"] != 22 || names["Gnoll 2"] != 22 {
			t.Errorf("tokens %v, want Gnoll and Gnoll 2 at 22 hp", names)
		}
	})
}
