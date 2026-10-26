// Package game is the facade every transport (WebSocket, REST, MCP) talks to.
// In-scene changes are scene.Commands applied through Apply/ApplyBatch (which
// resolve token templates, then hand off to Table); operations that swap the
// whole scene (new, load) and the token library live here because they also
// touch the store.
package game

import (
	"errors"
	"fmt"

	"github.com/charltonadam/tilefall/internal/scene"
	"github.com/charltonadam/tilefall/internal/store"
)

type Game struct {
	Table   *scene.Table
	Store   *store.Store
	Library *Library
}

// Open loads the token library and the last active scene, or creates a
// starter scene.
func Open(cat *scene.Catalog, st *store.Store) (*Game, error) {
	data, err := st.LoadLibrary()
	if err != nil {
		return nil, err
	}
	lib, err := openLibrary(data, st.SaveLibrary)
	if err != nil {
		return nil, err
	}
	if id := st.ActiveID(); id != "" {
		if sc, err := st.Load(id); err == nil {
			return &Game{Table: scene.NewTable(cat, sc), Store: st, Library: lib}, nil
		}
	}
	sc := scene.New(store.NewID(), scene.Meta{Name: "Untitled map", Width: 30, Height: 20, Grid: scene.DefaultGrid()})
	g := &Game{Table: scene.NewTable(cat, sc), Store: st, Library: lib}
	if err := st.Save(sc.ID, g.Table.Snapshot()); err != nil {
		return nil, err
	}
	return g, st.SetActive(sc.ID)
}

func (g *Game) Apply(cmd scene.Command) (scene.ApplyResult, error) {
	return g.ApplyBatch([]scene.Command{cmd})
}

// ApplyBatch fills add_token commands that name a templateId from the library,
// then applies the batch atomically. Transports call this rather than
// Table.ApplyBatch so templates work everywhere.
func (g *Game) ApplyBatch(cmds []scene.Command) (scene.ApplyResult, error) {
	resolved := make([]scene.Command, len(cmds))
	for i, c := range cmds {
		resolved[i] = c
		at, ok := c.(*scene.AddToken)
		if !ok || at.TemplateID == "" {
			continue
		}
		tpl, ok := g.Library.Get(at.TemplateID)
		if !ok {
			err := fmt.Errorf("no token template %q", at.TemplateID)
			if len(cmds) > 1 {
				err = fmt.Errorf("command %d (add_token): %w", i, err)
			}
			return scene.ApplyResult{}, err
		}
		cp := *at
		cp.FromTemplate(tpl)
		resolved[i] = &cp
	}
	return g.Table.ApplyBatch(resolved)
}

type NewSceneParams struct {
	Name           string               `json:"name"`
	Width          int                  `json:"width" jsonschema:"width in cells (1-200)"`
	Height         int                  `json:"height" jsonschema:"height in cells (1-200)"`
	GridType       scene.GridType       `json:"gridType,omitempty" jsonschema:"square (default) or hex"`
	HexOrientation scene.HexOrientation `json:"hexOrientation,omitempty" jsonschema:"pointy (default) or flat"`
	Terrain        string               `json:"terrain,omitempty" jsonschema:"terrain id to fill the whole map with (optional)"`
}

// NewScene creates a scene, makes it active and returns its id. The previous
// scene is saved and can be loaded again.
func (g *Game) NewScene(p NewSceneParams) (string, error) {
	grid := scene.DefaultGrid()
	if p.GridType != "" {
		grid.Type = p.GridType
	}
	if p.HexOrientation != "" {
		grid.HexOrientation = p.HexOrientation
	}
	if p.Name == "" {
		p.Name = "Untitled map"
	}
	m := scene.Meta{Name: p.Name, Width: p.Width, Height: p.Height, Grid: grid}
	if err := m.Validate(); err != nil {
		return "", err
	}
	cat := g.Table.Catalog()
	if p.Terrain != "" && cat.Terrain(p.Terrain) == nil {
		return "", fmt.Errorf("unknown terrain %q", p.Terrain)
	}
	sc := scene.New(store.NewID(), m)
	if p.Terrain != "" {
		for row := 0; row < m.Height; row++ {
			for col := 0; col < m.Width; col++ {
				sc.Terrain[scene.Cell{Col: col, Row: row}.Key()] = p.Terrain
			}
		}
	}
	if err := g.Store.Switch(g.Table, sc); err != nil {
		return "", err
	}
	return sc.ID, nil
}

func (g *Game) LoadScene(id string) error {
	if id == g.Table.SceneID() {
		return nil
	}
	g.Store.Flush(g.Table)
	sc, err := g.Store.Load(id)
	if err != nil {
		return err
	}
	return g.Store.Switch(g.Table, sc)
}

func (g *Game) DeleteScene(id string) error {
	if id == g.Table.SceneID() {
		return errors.New("cannot delete the active scene; load another one first")
	}
	return g.Store.Delete(id)
}

func (g *Game) ListScenes() ([]store.SceneInfo, error) {
	g.Store.Flush(g.Table)
	return g.Store.List()
}
