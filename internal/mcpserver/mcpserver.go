// Package mcpserver exposes the table to MCP clients (e.g. Claude Code) over
// streamable HTTP. Every mutating tool is a thin wrapper around a
// scene.Command applied through the shared Table, so browsers see agent
// changes live and agents get the same validation as the UI.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/charltonadam/tilefall/internal/game"
	"github.com/charltonadam/tilefall/internal/render"
	"github.com/charltonadam/tilefall/internal/scene"
)

const instructions = `Tile-Fall is a shared digital tabletop battlemap. Players are watching the map in their browsers; every change you make appears for them immediately.

Coordinates:
- Cells are addressed {col,row} from the top-left (0,0). This is true for square and hex grids (hex grids use offset coordinates: pointy-top shifts odd rows right, flat-top shifts odd columns down).
- Free positions (object x/y, wall points) are in grid units: 1 unit = one cell. On a square grid the center of cell {col,row} is (col+0.5,row+0.5) and integer coordinates are cell corners.
- Prefer giving objects a "cell" instead of x/y unless you need fine placement.

Building a map:
1. new_scene (size, grid type, base terrain) or get_scene_summary to see the current one.
2. list_catalog to see terrain ids, asset ids and wall styles. Only use ids from the catalog.
3. Lay down terrain broad to narrow: fill_terrain for areas, paint_path for rivers/roads/corridors.
4. Add walls (add_walls with rect for rooms) and structural objects (doors, pillars, stairs).
5. Dress the map: scatter_objects for natural clutter (trees on forest_floor, rocks, reeds by water), place_objects for deliberate set pieces. Use a seed and minSpacing.
6. Call render_preview to look at the result and refine. Use undo to revert a mistake.
Use apply_batch to send many commands as one atomic change (one undo step).

Tokens and the token library:
- The library (list_token_templates) holds reusable creature templates shared by all scenes. add_token with a templateId spawns a copy at full hp; repeated spawns are numbered (Goblin, Goblin 2...).
- A spawned token is independent: update_token changes only that token, and update_token_template changes only the defaults for future spawns.
- Track hit points with update_token hpDelta (negative for damage, positive for healing); hp is clamped to 0..maxHp. maxHp 0 means hp is not tracked.

During play: add_token / move_token / update_token manage creatures; tokens snap to cells. Use measure for movement and range questions (one cell is grid.distance, 5 ft by default; diagonals cost one cell).`

// Handler returns the HTTP handler serving MCP for g.
func Handler(g *game.Game) http.Handler {
	s := NewServer(g)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
}

func NewServer(g *game.Game) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "tilefall", Title: "Tile-Fall tabletop", Version: "0.1.0"},
		&mcp.ServerOptions{Instructions: instructions})
	addReadTools(s, g)
	addSceneTools(s, g)
	addLibraryTools(s, g)
	addCommandTools(s, g)
	return s
}

func jsonText(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		data = []byte(err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

type empty struct{}

// ---- read tools ----

type sceneSummary struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Version    uint64            `json:"version"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	Grid       scene.Grid        `json:"grid"`
	Background *scene.Background `json:"background,omitempty"`
	Terrain    map[string]int    `json:"terrainCellCounts"`
	Unpainted  int               `json:"unpaintedCells"`
	Objects    map[string]int    `json:"objectCountsByAsset"`
	Walls      int               `json:"walls"`
	Tokens     []*scene.Token    `json:"tokens"`
}

type previewArgs struct {
	PxPerCell float64 `json:"pxPerCell,omitempty" jsonschema:"pixels per cell (default: fit about 1400px)"`
	Labels    *bool   `json:"labels,omitempty" jsonschema:"draw col/row numbers around the edge (default true)"`
}

type endpoint struct {
	Token string      `json:"token,omitempty" jsonschema:"token id; measures from its anchor (top-left) cell"`
	Cell  *scene.Cell `json:"cell,omitempty"`
}

type measureArgs struct {
	From endpoint     `json:"from" jsonschema:"start: a token id or a cell"`
	To   endpoint     `json:"to" jsonschema:"end: a token id or a cell"`
	Via  []scene.Cell `json:"via,omitempty" jsonschema:"waypoints to pass through in order (default: shortest path)"`
}

type getSceneArgs struct {
	Include []string `json:"include,omitempty" jsonschema:"subset of terrain, objects, walls, tokens to include (default all)"`
}

func addReadTools(s *mcp.Server, g *game.Game) {
	mcp.AddTool(s, &mcp.Tool{Name: "get_scene_summary", Description: "Overview of the active scene: size, grid, terrain cell counts, object counts by asset, walls, and all tokens with their cells."},
		func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			var sum sceneSummary
			g.Table.Read(func(sc *scene.Scene) {
				sum = sceneSummary{ID: sc.ID, Name: sc.Name, Version: sc.Version, Width: sc.Width, Height: sc.Height, Grid: sc.Grid,
					Background: sc.Background, Terrain: map[string]int{}, Objects: map[string]int{}, Walls: len(sc.Walls), Tokens: []*scene.Token{}}
				for _, t := range sc.Terrain {
					sum.Terrain[t]++
				}
				sum.Unpainted = sc.Width*sc.Height - len(sc.Terrain)
				for _, o := range sc.Objects {
					sum.Objects[o.AssetID]++
				}
				for _, t := range sc.Tokens {
					sum.Tokens = append(sum.Tokens, t)
				}
			})
			sort.Slice(sum.Tokens, func(i, j int) bool { return sum.Tokens[i].ID < sum.Tokens[j].ID })
			return jsonText(sum), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_scene", Description: "Full JSON of the active scene (terrain is a map of \"col,row\" to terrain id). Can be large; use include to limit it."},
		func(_ context.Context, _ *mcp.CallToolRequest, in getSceneArgs) (*mcp.CallToolResult, any, error) {
			var out map[string]any
			if err := json.Unmarshal(g.Table.Snapshot(), &out); err != nil {
				return nil, nil, err
			}
			if len(in.Include) > 0 {
				for _, k := range []string{"terrain", "objects", "walls", "tokens"} {
					if !slices.Contains(in.Include, k) {
						delete(out, k)
					}
				}
			}
			return jsonText(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_catalog", Description: "Terrain types, placeable assets (with default size in cells and layer) and wall styles. Use these ids in other tools."},
		func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			cat := g.Table.Catalog()
			type asset struct {
				ID, Name, Description, Layer string
				Tags                         []string
				Size                         float64
			}
			type terrain struct {
				ID, Name, Description string
				Tags                  []string
			}
			out := struct {
				Terrains   []terrain
				Assets     []asset
				WallStyles []scene.WallStyle
			}{WallStyles: cat.WallStyles}
			for _, t := range cat.Terrains {
				out.Terrains = append(out.Terrains, terrain{t.ID, t.Name, t.Description, t.Tags})
			}
			for _, a := range cat.Assets {
				out.Assets = append(out.Assets, asset{a.ID, a.Name, a.Description, a.Layer, a.Tags, a.Size})
			}
			return jsonText(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "measure", Description: "Movement distance between two cells or tokens, optionally via waypoints. Uses the scene's scale (D&D default: 5 ft per cell, diagonals cost the same as straight moves). Ignores walls and difficult terrain."},
		func(_ context.Context, _ *mcp.CallToolRequest, in measureArgs) (*mcp.CallToolResult, any, error) {
			var out map[string]any
			var err error
			g.Table.Read(func(sc *scene.Scene) {
				resolve := func(e endpoint, which string) (scene.Cell, bool) {
					if e.Token != "" {
						t, ok := sc.Tokens[e.Token]
						if !ok {
							err = fmt.Errorf("%s: no token %q", which, e.Token)
							return scene.Cell{}, false
						}
						return t.Cell, true
					}
					if e.Cell == nil {
						err = fmt.Errorf("%s needs a token or a cell", which)
						return scene.Cell{}, false
					}
					return *e.Cell, true
				}
				from, ok1 := resolve(in.From, "from")
				to, ok2 := resolve(in.To, "to")
				if !ok1 || !ok2 {
					return
				}
				path := append(append([]scene.Cell{from}, in.Via...), to)
				steps := 0
				for i := 1; i < len(path); i++ {
					steps += sc.Grid.Steps(path[i-1], path[i])
				}
				per, units := sc.Grid.Scale()
				out = map[string]any{"cells": steps, "distance": float64(steps) * per, "units": units}
			})
			if err != nil {
				return nil, nil, err
			}
			return jsonText(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "render_preview", Description: "Render the active scene as a PNG so you can see it. Assets are drawn as simple colored shapes (the real artwork is shown in browsers); tokens are white-ringed circles with their initial. Column and row numbers are drawn around the edge."},
		func(_ context.Context, _ *mcp.CallToolRequest, in previewArgs) (*mcp.CallToolResult, any, error) {
			labels := in.Labels == nil || *in.Labels
			var png []byte
			var err error
			g.Table.Read(func(sc *scene.Scene) {
				png, err = render.PNG(sc, g.Table.Catalog(), render.Options{PxPerCell: in.PxPerCell, MaxPx: 1400, Labels: labels})
			})
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: png, MIMEType: "image/png"}}}, nil, nil
		})
}

// ---- scene management ----

type idArgs struct {
	ID string `json:"id"`
}

func addSceneTools(s *mcp.Server, g *game.Game) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_scenes", Description: "List saved scenes, most recently modified first."},
		func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			list, err := g.ListScenes()
			if err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]any{"active": g.Table.SceneID(), "scenes": list}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "new_scene", Description: "Create a new empty scene and switch every player to it. The current scene is saved first and can be loaded again with load_scene."},
		func(_ context.Context, _ *mcp.CallToolRequest, in game.NewSceneParams) (*mcp.CallToolResult, any, error) {
			id, err := g.NewScene(in)
			if err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]string{"id": id}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "load_scene", Description: "Switch every player to a saved scene."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idArgs) (*mcp.CallToolResult, any, error) {
			if err := g.LoadScene(in.ID); err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]string{"active": in.ID}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "undo", Description: "Undo the most recent change to the active scene (an apply_batch call is one change)."},
		func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			v, err := g.Table.Undo()
			if err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]uint64{"version": v}), nil, nil
		})
}

// ---- token library ----

func addLibraryTools(s *mcp.Server, g *game.Game) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_token_templates", Description: "List the token library: reusable creature templates (name, size, color, image, maxHp) to spawn with add_token templateId."},
		func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			return jsonText(map[string]any{"templates": g.Library.List()}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "create_token_template", Description: "Add a creature template to the token library. Returns its id."},
		func(_ context.Context, _ *mcp.CallToolRequest, in game.NewTemplateParams) (*mcp.CallToolResult, any, error) {
			id, err := g.Library.Create(in)
			if err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]string{"id": id}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "update_token_template", Description: "Change a library template's defaults. Only affects tokens spawned afterwards; tokens already on the map keep their values."},
		func(_ context.Context, _ *mcp.CallToolRequest, in game.TemplateUpdate) (*mcp.CallToolResult, any, error) {
			if err := g.Library.Update(in); err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]bool{"ok": true}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_token_template", Description: "Remove a template from the token library. Tokens already spawned from it stay on the map."},
		func(_ context.Context, _ *mcp.CallToolRequest, in idArgs) (*mcp.CallToolResult, any, error) {
			if err := g.Library.Delete(in.ID); err != nil {
				return nil, nil, err
			}
			return jsonText(map[string]bool{"ok": true}), nil, nil
		})
}

// ---- commands ----

// addCommand registers a tool whose input is the command struct itself.
func addCommand[T any, P interface {
	*T
	scene.Command
}](s *mcp.Server, g *game.Game, desc string) {
	name := P(new(T)).Type()
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc},
		func(_ context.Context, _ *mcp.CallToolRequest, in T) (*mcp.CallToolResult, any, error) {
			res, err := g.Apply(P(&in))
			if err != nil {
				return nil, nil, err
			}
			return jsonText(res), nil, nil
		})
}

const regionHelp = " A region sets exactly one of: all, rect {from,to} (inclusive cells), cells [...], polygon [cells as vertices], circle {center, radius in cells}."

type batchArgs struct {
	Commands []map[string]any `json:"commands" jsonschema:"commands to apply in order; each is an object with a \"type\" field naming a tool (e.g. fill_terrain) plus that tool's arguments"`
}

func addCommandTools(s *mcp.Server, g *game.Game) {
	addCommand[scene.UpdateScene](s, g, "Change scene settings: name, size in cells, grid (type square/hex, hexOrientation, cellSize, visible, color, opacity, distance per cell and units) or background image.")
	addCommand[scene.FillTerrain](s, g, "Paint terrain over a region of cells. Use terrain \"\" to erase."+regionHelp)
	addCommand[scene.PaintPath](s, g, "Paint terrain along a path through cell centers with a width in cells: rivers, roads, corridors, trails.")
	addCommand[scene.PlaceObjects](s, g, "Place one or more catalog assets (trees, furniture, doors...). Give each a cell (centered on it) or x/y in grid units, plus optional rotation (degrees), scale and layer. Returns the new object ids.")
	addCommand[scene.UpdateObject](s, g, "Move, rotate, rescale or re-layer an existing object.")
	addCommand[scene.RemoveObjects](s, g, "Remove objects by id.")
	addCommand[scene.ScatterObjects](s, g, "Randomly scatter assets across a region, e.g. trees in a forest or rocks in a cave. Deterministic for a given seed. Use onTerrain to restrict placement and minSpacing to avoid overlap."+regionHelp)
	addCommand[scene.AddWalls](s, g, "Add walls as polylines in grid units (square grid: integer coordinates are cell corners), or outline a rectangle of cells with rect. Leave gaps for doors by using separate polylines. Returns wall ids.")
	addCommand[scene.RemoveWalls](s, g, "Remove walls by id.")
	addCommand[scene.AddToken](s, g, "Add a creature/character token on a cell (its top-left cell if larger than 1), either from a library template (templateId) or from scratch (name). It starts at full hp. Returns its id.")
	addCommand[scene.MoveToken](s, g, "Move a token to a cell.")
	addCommand[scene.UpdateToken](s, g, "Change one token on the map: name, size, color, image, maxHp, hp, or hpDelta for damage/healing. Does not change its library template.")
	addCommand[scene.RemoveTokens](s, g, "Remove tokens by id.")
	addCommand[scene.ClearRegion](s, g, "Clear terrain, objects, walls and/or tokens inside a region."+regionHelp)

	mcp.AddTool(s, &mcp.Tool{Name: "apply_batch", Description: "Apply several commands atomically as a single change and single undo step. If any command fails nothing is applied. Results are returned in order."},
		func(_ context.Context, _ *mcp.CallToolRequest, in batchArgs) (*mcp.CallToolResult, any, error) {
			cmds := make([]scene.Command, len(in.Commands))
			for i, raw := range in.Commands {
				data, _ := json.Marshal(raw)
				c, err := scene.DecodeCommand(data)
				if err != nil {
					return nil, nil, fmt.Errorf("commands[%d]: %w", i, err)
				}
				cmds[i] = c
			}
			res, err := g.ApplyBatch(cmds)
			if err != nil {
				return nil, nil, err
			}
			return jsonText(res), nil, nil
		})
}
