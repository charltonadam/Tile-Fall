package scene

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/grid_vectors.json")

var testGrids = map[string]Grid{
	"square":     {Type: GridSquare, CellSize: 70},
	"hex-pointy": {Type: GridHex, HexOrientation: HexPointy, CellSize: 70},
	"hex-flat":   {Type: GridHex, HexOrientation: HexFlat, CellSize: 70},
}

func TestCenterRoundTrip(t *testing.T) {
	for name, g := range testGrids {
		for col := -3; col < 12; col++ {
			for row := -3; row < 12; row++ {
				c := Cell{col, row}
				ctr := g.CellCenter(c)
				if got := g.PointToCell(ctr); got != c {
					t.Errorf("%s: PointToCell(center(%v)) = %v", name, c, got)
				}
				// points just inside each corner still belong to the cell
				for _, k := range g.CellCorners(c) {
					in := Point{ctr.X + (k.X-ctr.X)*0.9, ctr.Y + (k.Y-ctr.Y)*0.9}
					if got := g.PointToCell(in); got != c {
						t.Errorf("%s: point near corner of %v maps to %v", name, c, got)
					}
				}
			}
		}
	}
}

func TestSteps(t *testing.T) {
	// every cell at distance 1 is an actual neighbor (6 on hex, 8 on square:
	// diagonals count as one 5 ft step), and steps are symmetric
	for name, g := range testGrids {
		for _, a := range []Cell{{4, 4}, {5, 5}, {4, 5}} {
			ctr := g.CellCenter(a)
			n := 0
			for col := a.Col - 3; col <= a.Col+3; col++ {
				for row := a.Row - 3; row <= a.Row+3; row++ {
					b := Cell{col, row}
					if g.Steps(a, b) != g.Steps(b, a) {
						t.Errorf("%s: steps %v<->%v not symmetric", name, a, b)
					}
					if g.Steps(a, b) == 1 {
						n++
						if d := dist(ctr, g.CellCenter(b)); d > math.Sqrt2+1e-9 {
							t.Errorf("%s: %v and %v are 1 step apart but %v units", name, a, b, d)
						}
					}
				}
			}
			want := 8
			if g.isHex() {
				want = 6
			}
			if n != want {
				t.Errorf("%s: %v has %d cells at 1 step, want %d", name, a, n, want)
			}
		}
	}
}

func TestHexNeighborSpacing(t *testing.T) {
	// adjacent hex centers are exactly one grid unit apart
	for name, g := range testGrids {
		if !g.isHex() {
			continue
		}
		a := g.CellCenter(Cell{4, 4})
		near := 0
		for col := 2; col <= 6; col++ {
			for row := 2; row <= 6; row++ {
				if d := dist(a, g.CellCenter(Cell{col, row})); math.Abs(d-1) < 1e-9 {
					near++
				}
			}
		}
		if near != 6 {
			t.Errorf("%s: %d neighbors at distance 1, want 6", name, near)
		}
	}
}

type gridVector struct {
	Grid    string `json:"grid"`
	Cell    Cell   `json:"cell"`
	Center  Point  `json:"center"`
	Probe   Point  `json:"probe"`
	ProbeIn Cell   `json:"probeCell"`
}

type stepVector struct {
	Grid  string `json:"grid"`
	A     Cell   `json:"a"`
	B     Cell   `json:"b"`
	Steps int    `json:"steps"`
}

type vectorFile struct {
	Grids   map[string]Grid  `json:"grids"`
	Extents map[string]Point `json:"extents"` // for a 7x5 map
	Vectors []gridVector     `json:"vectors"`
	Steps   []stepVector     `json:"steps"`
}

// TestVectors pins grid math to testdata/grid_vectors.json, which the
// TypeScript tests (web/src/lib/grid.test.ts) also check.
func TestVectors(t *testing.T) {
	vf := vectorFile{Grids: testGrids, Extents: map[string]Point{}}
	for _, name := range []string{"square", "hex-pointy", "hex-flat"} {
		g := testGrids[name]
		vf.Extents[name] = g.MapExtent(7, 5)
		for _, c := range []Cell{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {3, 2}, {4, 3}, {-1, -1}, {6, 4}} {
			probe := Point{float64(c.Col)*0.731 + 0.37, float64(c.Row)*0.913 + 0.61}
			vf.Vectors = append(vf.Vectors, gridVector{name, c, g.CellCenter(c), probe, g.PointToCell(probe)})
		}
		for _, p := range [][2]Cell{{{0, 0}, {3, 0}}, {{0, 0}, {3, 3}}, {{1, 1}, {4, 2}}, {{2, 3}, {2, 0}}, {{1, 2}, {5, 7}}, {{3, 3}, {0, 6}}, {{4, 1}, {1, 4}}, {{0, 1}, {1, 0}}} {
			vf.Steps = append(vf.Steps, stepVector{name, p[0], p[1], g.Steps(p[0], p[1])})
		}
	}
	path := filepath.Join("testdata", "grid_vectors.json")
	got, _ := json.MarshalIndent(vf, "", "  ")
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	var wantVF vectorFile
	if err := json.Unmarshal(want, &wantVF); err != nil {
		t.Fatal(err)
	}
	for i, v := range wantVF.Vectors {
		g := testGrids[v.Grid]
		c := g.CellCenter(v.Cell)
		if math.Abs(c.X-v.Center.X) > 1e-9 || math.Abs(c.Y-v.Center.Y) > 1e-9 || g.PointToCell(v.Probe) != v.ProbeIn {
			t.Errorf("vector %d (%s %v) no longer matches; grid math changed — update grid.ts too, then rerun with -update", i, v.Grid, v.Cell)
		}
	}
}
