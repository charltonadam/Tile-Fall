package scene

import (
	"errors"
	"math"
)

// Region selects a set of cells. Exactly one field must be set.
type Region struct {
	All     bool      `json:"all,omitempty" jsonschema:"select every cell on the map"`
	Rect    *CellRect `json:"rect,omitempty" jsonschema:"inclusive rectangle of cells"`
	Cells   []Cell    `json:"cells,omitempty" jsonschema:"explicit list of cells"`
	Polygon []Cell    `json:"polygon,omitempty" jsonschema:"polygon whose vertices are the centers of these cells (3 or more); selects every cell whose center is inside or on the edge"`
	Circle  *Circle   `json:"circle,omitempty" jsonschema:"cells whose centers are within radius of the center cell's center"`
}

type CellRect struct {
	From Cell `json:"from" jsonschema:"one corner cell"`
	To   Cell `json:"to" jsonschema:"opposite corner cell (inclusive)"`
}

type Circle struct {
	Center Cell    `json:"center"`
	Radius float64 `json:"radius" jsonschema:"radius in cells; 0 selects only the center"`
}

// Resolve returns the in-bounds cells selected by r, without duplicates.
func (r Region) Resolve(s *Scene) ([]Cell, error) {
	set := 0
	for _, b := range []bool{r.All, r.Rect != nil, r.Cells != nil, r.Polygon != nil, r.Circle != nil} {
		if b {
			set++
		}
	}
	if set != 1 {
		return nil, errors.New("region must set exactly one of all, rect, cells, polygon, circle")
	}
	var out []Cell
	switch {
	case r.All:
		out = s.filter(func(Cell) bool { return true })
	case r.Rect != nil:
		c0, c1 := min(r.Rect.From.Col, r.Rect.To.Col), max(r.Rect.From.Col, r.Rect.To.Col)
		r0, r1 := min(r.Rect.From.Row, r.Rect.To.Row), max(r.Rect.From.Row, r.Rect.To.Row)
		out = s.filter(func(c Cell) bool { return c.Col >= c0 && c.Col <= c1 && c.Row >= r0 && c.Row <= r1 })
	case r.Cells != nil:
		seen := map[Cell]bool{}
		for _, c := range r.Cells {
			if s.InBounds(c) && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	case r.Polygon != nil:
		if len(r.Polygon) < 3 {
			return nil, errors.New("polygon needs at least 3 vertices")
		}
		poly := make([]Point, len(r.Polygon))
		for i, c := range r.Polygon {
			poly[i] = s.Grid.CellCenter(c)
		}
		out = s.filter(func(c Cell) bool {
			p := s.Grid.CellCenter(c)
			return pointInPolygon(p, poly) || distToPolyline(p, poly, true) < 1e-6
		})
	case r.Circle != nil:
		if r.Circle.Radius < 0 {
			return nil, errors.New("circle radius must be >= 0")
		}
		ctr := s.Grid.CellCenter(r.Circle.Center)
		out = s.filter(func(c Cell) bool { return dist(s.Grid.CellCenter(c), ctr) <= r.Circle.Radius+1e-6 })
	}
	return out, nil
}

func (s *Scene) filter(keep func(Cell) bool) []Cell {
	var out []Cell
	for row := 0; row < s.Height; row++ {
		for col := 0; col < s.Width; col++ {
			if c := (Cell{col, row}); keep(c) {
				out = append(out, c)
			}
		}
	}
	return out
}

func dist(a, b Point) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func pointInPolygon(p Point, poly []Point) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a.Y > p.Y) != (b.Y > p.Y) && p.X < (b.X-a.X)*(p.Y-a.Y)/(b.Y-a.Y)+a.X {
			in = !in
		}
	}
	return in
}

func distToSegment(p, a, b Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return dist(p, a)
	}
	t := math.Max(0, math.Min(1, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/l2))
	return dist(p, Point{a.X + t*dx, a.Y + t*dy})
}

func distToPolyline(p Point, pts []Point, closed bool) float64 {
	if len(pts) == 1 {
		return dist(p, pts[0])
	}
	d := math.Inf(1)
	for i := 0; i+1 < len(pts); i++ {
		d = math.Min(d, distToSegment(p, pts[i], pts[i+1]))
	}
	if closed && len(pts) > 2 {
		d = math.Min(d, distToSegment(p, pts[len(pts)-1], pts[0]))
	}
	return d
}
