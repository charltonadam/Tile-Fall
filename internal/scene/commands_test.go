package scene

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := ParseCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func newTestTable(t *testing.T, g Grid) *Table {
	return NewTable(testCatalog(t), New("s1", Meta{Name: "test", Width: 10, Height: 8, Grid: g}))
}

func mustApply(t *testing.T, tb *Table, cmd Command) ApplyResult {
	t.Helper()
	res, err := tb.Apply(cmd)
	if err != nil {
		t.Fatalf("%s: %v", cmd.Type(), err)
	}
	return res
}

func sceneOf(tb *Table) *Scene {
	var out *Scene
	tb.Read(func(s *Scene) { out = s.Clone() })
	return out
}

func TestCatalogFilesExist(t *testing.T) {
	for _, a := range testCatalog(t).Assets {
		if _, err := os.Stat(filepath.Join("..", "..", "assets", a.File)); err != nil {
			t.Errorf("asset %s: %v", a.ID, err)
		}
	}
}

func TestFillTerrainAndUndo(t *testing.T) {
	tb := newTestTable(t, DefaultGrid())
	mustApply(t, tb, &FillTerrain{Region: Region{All: true}, Terrain: "grass"})
	mustApply(t, tb, &FillTerrain{Region: Region{Rect: &CellRect{From: Cell{2, 2}, To: Cell{0, 0}}}, Terrain: "water_deep"})
	s := sceneOf(tb)
	if len(s.Terrain) != 80 || s.Terrain["1,1"] != "water_deep" || s.Terrain["3,3"] != "grass" {
		t.Fatalf("unexpected terrain: %d cells, 1,1=%q", len(s.Terrain), s.Terrain["1,1"])
	}
	if _, err := tb.Undo(); err != nil {
		t.Fatal(err)
	}
	if s := sceneOf(tb); s.Terrain["1,1"] != "grass" {
		t.Fatalf("undo failed: %q", s.Terrain["1,1"])
	}
	if _, err := tb.Apply(&FillTerrain{Region: Region{All: true}, Terrain: "nope"}); err == nil {
		t.Fatal("expected unknown terrain error")
	}
}

func TestRegionShapes(t *testing.T) {
	s := New("x", Meta{Width: 10, Height: 10, Grid: DefaultGrid()})
	count := func(r Region) int {
		cells, err := r.Resolve(s)
		if err != nil {
			t.Fatal(err)
		}
		return len(cells)
	}
	if n := count(Region{Circle: &Circle{Center: Cell{5, 5}, Radius: 1}}); n != 5 {
		t.Errorf("circle r=1: %d cells, want 5", n)
	}
	// triangle with vertices at cell centers includes its edges
	if n := count(Region{Polygon: []Cell{{0, 0}, {4, 0}, {0, 4}}}); n != 15 {
		t.Errorf("triangle: %d cells, want 15", n)
	}
	if n := count(Region{Cells: []Cell{{1, 1}, {1, 1}, {50, 50}}}); n != 1 {
		t.Errorf("cells dedupe/bounds: %d, want 1", n)
	}
	if _, err := (Region{All: true, Cells: []Cell{{0, 0}}}).Resolve(s); err == nil {
		t.Error("expected error for two region kinds")
	}
}

func TestPaintPath(t *testing.T) {
	tb := newTestTable(t, DefaultGrid())
	mustApply(t, tb, &PaintPath{Points: []Cell{{0, 3}, {9, 3}}, Width: 3, Terrain: "water_shallow"})
	s := sceneOf(tb)
	if len(s.Terrain) != 30 {
		t.Fatalf("width-3 river across 10 cols: %d cells, want 30", len(s.Terrain))
	}
}

func TestObjectsAndScatterDeterministic(t *testing.T) {
	run := func() []Object {
		tb := newTestTable(t, Grid{Type: GridHex, HexOrientation: HexFlat, CellSize: 70})
		mustApply(t, tb, &FillTerrain{Region: Region{All: true}, Terrain: "grass"})
		mustApply(t, tb, &FillTerrain{Region: Region{Rect: &CellRect{From: Cell{0, 0}, To: Cell{4, 7}}}, Terrain: "forest_floor"})
		res := mustApply(t, tb, &ScatterObjects{Region: Region{All: true}, AssetIDs: []string{"tree_oak", "tree_pine"}, Count: 12, Seed: 42, MinSpacing: 1, OnTerrain: []string{"forest_floor"}})
		sr := res.Result.(ScatterResult)
		if sr.Placed == 0 {
			t.Fatal("nothing placed")
		}
		s := sceneOf(tb)
		var out []Object
		for _, id := range sr.IDs {
			o := s.Objects[id]
			if c := s.Grid.PointToCell(Point{o.X, o.Y}); s.Terrain[c.Key()] != "forest_floor" {
				t.Errorf("object %s landed on %q", id, s.Terrain[c.Key()])
			}
			out = append(out, *o)
		}
		return out
	}
	if a, b := run(), run(); !reflect.DeepEqual(a, b) {
		t.Fatal("scatter with the same seed differed")
	}
}

func TestBatchIsAtomic(t *testing.T) {
	tb := newTestTable(t, DefaultGrid())
	_, err := tb.ApplyBatch([]Command{
		&FillTerrain{Region: Region{All: true}, Terrain: "grass"},
		&AddToken{Name: "Goblin", Cell: Cell{99, 99}},
	})
	if err == nil {
		t.Fatal("expected out of bounds error")
	}
	if s := sceneOf(tb); len(s.Terrain) != 0 || s.Version != 0 {
		t.Fatalf("batch was not rolled back: %d terrain, version %d", len(s.Terrain), s.Version)
	}

	res, err := tb.ApplyBatch([]Command{
		&AddToken{Name: "Fighter", Cell: Cell{1, 1}},
		&FillTerrain{Region: Region{All: true}, Terrain: "dirt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Result.([]any)[0].(IDResult).ID
	mustApply(t, tb, &MoveToken{ID: id, Cell: Cell{5, 5}})
	tb.Undo()
	if s := sceneOf(tb); s.Tokens[id].Cell != (Cell{1, 1}) {
		t.Fatalf("undo move: %v", s.Tokens[id].Cell)
	}
	tb.Undo() // undoes the whole batch
	if s := sceneOf(tb); len(s.Tokens) != 0 || len(s.Terrain) != 0 {
		t.Fatal("undo of batch left state behind")
	}
}

func TestSubscriberGetsPatches(t *testing.T) {
	tb := newTestTable(t, DefaultGrid())
	sub, snap := tb.Subscribe()
	if _, err := Decode(snap); err != nil {
		t.Fatal(err)
	}
	mustApply(t, tb, &AddWalls{Walls: []WallSpec{{Rect: &CellRect{From: Cell{1, 1}, To: Cell{3, 2}}}}})
	ev := <-sub.C
	var p Patch
	if err := json.Unmarshal(ev.Data, &p); err != nil || ev.Kind != "patch" || p.Version != 1 || len(p.Walls) != 1 {
		t.Fatalf("bad event %s %s", ev.Kind, ev.Data)
	}
	for _, w := range p.Walls {
		want := []Point{{1, 1}, {4, 1}, {4, 3}, {1, 3}}
		if !reflect.DeepEqual(w.Points, want) {
			t.Errorf("rect wall points %v, want %v", w.Points, want)
		}
	}
}

func TestDecodeCommand(t *testing.T) {
	cmd, err := DecodeCommand([]byte(`{"type":"fill_terrain","region":{"circle":{"center":{"col":2,"row":2},"radius":1.5}},"terrain":"sand"}`))
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := cmd.(*FillTerrain); !ok || f.Region.Circle.Radius != 1.5 {
		t.Fatalf("decoded %#v", cmd)
	}
	if _, err := DecodeCommand([]byte(`{"type":"explode"}`)); err == nil {
		t.Fatal("expected unknown type error")
	}
}

func TestTokenHP(t *testing.T) {
	tb := newTestTable(t, DefaultGrid())
	id := mustApply(t, tb, &AddToken{Name: "Orc", Cell: Cell{1, 1}, MaxHP: 15}).Result.(IDResult).ID
	if tok := sceneOf(tb).Tokens[id]; tok.HP != 15 || tok.MaxHP != 15 {
		t.Fatalf("new token hp %d/%d, want 15/15", tok.HP, tok.MaxHP)
	}
	ptr := func(v int) *int { return &v }
	for _, tc := range []struct {
		cmd  UpdateToken
		want int
	}{
		{UpdateToken{HPDelta: ptr(-6)}, 9},
		{UpdateToken{HPDelta: ptr(-20)}, 0},
		{UpdateToken{HPDelta: ptr(100)}, 15},
		{UpdateToken{HP: ptr(12)}, 12},
		{UpdateToken{MaxHP: ptr(10)}, 10},
		{UpdateToken{HP: ptr(4), HPDelta: ptr(1)}, 5},
	} {
		tc.cmd.ID = id
		mustApply(t, tb, &tc.cmd)
		if got := sceneOf(tb).Tokens[id].HP; got != tc.want {
			t.Errorf("after %+v: hp %d, want %d", tc.cmd, got, tc.want)
		}
	}
	if _, err := tb.Apply(&UpdateToken{ID: id, MaxHP: ptr(-1)}); err == nil {
		t.Error("negative maxHp accepted")
	}
}

func TestTemplateSpawnsAreNumbered(t *testing.T) {
	tb := newTestTable(t, DefaultGrid())
	tpl := &TokenTemplate{ID: "tpl1", Name: "Goblin", Size: 1, Color: "#4f8a3a", MaxHP: 7}
	var cmds []Command
	for i := range 3 {
		c := &AddToken{Cell: Cell{i, 0}}
		c.FromTemplate(tpl)
		cmds = append(cmds, c)
	}
	res, err := tb.ApplyBatch(cmds)
	if err != nil {
		t.Fatal(err)
	}
	s := sceneOf(tb)
	var names []string
	for _, r := range res.Result.([]any) {
		tok := s.Tokens[r.(IDResult).ID]
		if tok.HP != 7 || tok.Color != "#4f8a3a" || tok.TemplateID != "tpl1" {
			t.Errorf("token %+v did not copy the template", tok)
		}
		names = append(names, tok.Name)
	}
	if want := []string{"Goblin", "Goblin 2", "Goblin 3"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names %v, want %v", names, want)
	}
}
