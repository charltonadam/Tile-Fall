package scene

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// World coordinates are in "grid units": one unit is the distance between the
// centers of two adjacent cells (the side of a square cell, or the
// flat-to-flat width of a hex). Pixels only appear at render time
// (units * Grid.CellSize), so changing CellSize never moves anything.
//
// Cells are always addressed as (col, row). For hex grids these are offset
// coordinates: "odd-r" for pointy-top (odd rows shifted right half a cell) and
// "odd-q" for flat-top (odd columns shifted down half a cell). Axial/cube
// coordinates are only used internally for rounding.
//
// web/src/lib/grid.ts mirrors this file; testdata/grid_vectors.json keeps the two
// in agreement (regenerate with `go test ./internal/scene -run Vectors -update`).

type GridType string

const (
	GridSquare GridType = "square"
	GridHex    GridType = "hex"
)

type HexOrientation string

const (
	HexPointy HexOrientation = "pointy"
	HexFlat   HexOrientation = "flat"
)

type Grid struct {
	Type           GridType       `json:"type" jsonschema:"square or hex"`
	HexOrientation HexOrientation `json:"hexOrientation,omitempty" jsonschema:"pointy or flat (hex only)"`
	CellSize       int            `json:"cellSize" jsonschema:"pixels per cell at 100% zoom"`
	Visible        bool           `json:"visible"`
	Color          string         `json:"color" jsonschema:"grid line color, CSS hex like #000000"`
	Opacity        float64        `json:"opacity" jsonschema:"grid line opacity 0..1"`
	// Distance and Units say how far one cell is for measuring (D&D: 5 ft).
	// Zero values mean the defaults, so scenes saved before these existed still
	// measure in 5 ft cells.
	Distance float64 `json:"distance,omitempty" jsonschema:"distance one cell represents, default 5"`
	Units    string  `json:"units,omitempty" jsonschema:"distance unit label, default ft"`
}

const (
	DefaultCellDistance = 5
	DefaultUnits        = "ft"
)

func DefaultGrid() Grid {
	return Grid{Type: GridSquare, HexOrientation: HexPointy, CellSize: 70, Visible: true, Color: "#000000", Opacity: 0.35, Distance: DefaultCellDistance, Units: DefaultUnits}
}

func (g Grid) Validate() error {
	switch g.Type {
	case GridSquare:
	case GridHex:
		if g.HexOrientation != HexPointy && g.HexOrientation != HexFlat {
			return fmt.Errorf("hexOrientation must be %q or %q", HexPointy, HexFlat)
		}
	default:
		return fmt.Errorf("grid type must be %q or %q", GridSquare, GridHex)
	}
	if g.CellSize < 10 || g.CellSize > 500 {
		return fmt.Errorf("cellSize must be between 10 and 500")
	}
	if g.Opacity < 0 || g.Opacity > 1 {
		return fmt.Errorf("opacity must be between 0 and 1")
	}
	if g.Distance < 0 || g.Distance > 10000 {
		return fmt.Errorf("distance must be between 0 and 10000")
	}
	if len(g.Units) > 12 {
		return fmt.Errorf("units must be at most 12 characters")
	}
	return nil
}

type Cell struct {
	Col int `json:"col"`
	Row int `json:"row"`
}

func (c Cell) Key() string { return strconv.Itoa(c.Col) + "," + strconv.Itoa(c.Row) }

func ParseCellKey(k string) (Cell, error) {
	a, b, ok := strings.Cut(k, ",")
	if !ok {
		return Cell{}, fmt.Errorf("bad cell key %q", k)
	}
	col, err1 := strconv.Atoi(a)
	row, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return Cell{}, fmt.Errorf("bad cell key %q", k)
	}
	return Cell{col, row}, nil
}

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

const sqrt3 = 1.7320508075688772

// hexR is the hex radius (center to corner) in grid units.
const hexR = 1 / sqrt3

func (g Grid) isHex() bool  { return g.Type == GridHex }
func (g Grid) isFlat() bool { return g.Type == GridHex && g.HexOrientation == HexFlat }

// CellCenter returns the center of a cell in grid units.
func (g Grid) CellCenter(c Cell) Point {
	col, row := float64(c.Col), float64(c.Row)
	switch {
	case g.isFlat():
		return Point{1.5*hexR*col + hexR, row + 0.5*float64(c.Col&1) + 0.5}
	case g.isHex():
		return Point{col + 0.5*float64(c.Row&1) + 0.5, 1.5*hexR*row + hexR}
	default:
		return Point{col + 0.5, row + 0.5}
	}
}

// PointToCell returns the cell containing p (which may be out of bounds).
func (g Grid) PointToCell(p Point) Cell {
	if !g.isHex() {
		return Cell{int(math.Floor(p.X)), int(math.Floor(p.Y))}
	}
	var q, r float64
	if g.isFlat() {
		px, py := p.X-hexR, p.Y-0.5
		q = (2.0 / 3 * px) / hexR
		r = (-1.0/3*px + sqrt3/3*py) / hexR
	} else {
		px, py := p.X-0.5, p.Y-hexR
		q = (sqrt3/3*px - 1.0/3*py) / hexR
		r = (2.0 / 3 * py) / hexR
	}
	aq, ar := axialRound(q, r)
	if g.isFlat() {
		return Cell{aq, ar + (aq-(aq&1))/2}
	}
	return Cell{aq + (ar-(ar&1))/2, ar}
}

func axialRound(q, r float64) (int, int) {
	s := -q - r
	rq, rr, rs := math.Round(q), math.Round(r), math.Round(s)
	dq, dr, ds := math.Abs(rq-q), math.Abs(rr-r), math.Abs(rs-s)
	if dq > dr && dq > ds {
		rq = -rr - rs
	} else if dr > ds {
		rr = -rq - rs
	}
	return int(rq), int(rr)
}

// CellCorners returns the polygon outline of a cell in grid units.
func (g Grid) CellCorners(c Cell) []Point {
	ctr := g.CellCenter(c)
	if !g.isHex() {
		return []Point{{ctr.X - 0.5, ctr.Y - 0.5}, {ctr.X + 0.5, ctr.Y - 0.5}, {ctr.X + 0.5, ctr.Y + 0.5}, {ctr.X - 0.5, ctr.Y + 0.5}}
	}
	start := -30.0
	if g.isFlat() {
		start = 0
	}
	pts := make([]Point, 6)
	for i := range pts {
		a := (start + 60*float64(i)) * math.Pi / 180
		pts[i] = Point{ctr.X + hexR*math.Cos(a), ctr.Y + hexR*math.Sin(a)}
	}
	return pts
}

// MapExtent returns the width and height in grid units of a map that is
// w by h cells.
func (g Grid) MapExtent(w, h int) Point {
	fw, fh := float64(w), float64(h)
	switch {
	case g.isFlat():
		return Point{1.5*hexR*(fw-1) + 2*hexR, fh + 0.5}
	case g.isHex():
		return Point{fw + 0.5, 1.5*hexR*(fh-1) + 2*hexR}
	default:
		return Point{fw, fh}
	}
}

// CellDistance is how far one cell is, in Scale units.
func (g Grid) CellDistance() float64 {
	if g.Distance <= 0 {
		return DefaultCellDistance
	}
	return g.Distance
}

// Scale returns the distance per cell and its unit label, applying defaults.
func (g Grid) Scale() (float64, string) {
	u := g.Units
	if u == "" {
		u = DefaultUnits
	}
	return g.CellDistance(), u
}

// Steps is the number of single-cell moves between a and b. Square grids use
// the D&D 5e rule that diagonals cost the same as orthogonal moves
// (Chebyshev distance); hex grids use hex distance.
func (g Grid) Steps(a, b Cell) int {
	if !g.isHex() {
		return max(abs(a.Col-b.Col), abs(a.Row-b.Row))
	}
	aq, ar := g.toAxial(a)
	bq, br := g.toAxial(b)
	dq, dr := aq-bq, ar-br
	return (abs(dq) + abs(dr) + abs(dq+dr)) / 2
}

func (g Grid) toAxial(c Cell) (q, r int) {
	if g.isFlat() {
		return c.Col, c.Row - (c.Col-(c.Col&1))/2
	}
	return c.Col - (c.Row-(c.Row&1))/2, c.Row
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
