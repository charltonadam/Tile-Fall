// Package render draws a simplified top-down PNG of a scene for agents (and
// thumbnails). Assets are drawn as their catalog color and shape rather than
// their artwork, and coordinates are labelled along the edges so an agent can
// relate what it sees to cells.
package render

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"slices"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/charltonadam/tilefall/internal/scene"
)

type Options struct {
	// PxPerCell is the rendered size of one grid unit. 0 picks a size that
	// fits the map in about MaxPx pixels.
	PxPerCell float64
	MaxPx     int
	// Labels draws column/row numbers in a margin.
	Labels bool
}

const margin = 24

var (
	bgColor        = color.RGBA{0x1b, 0x1b, 0x1f, 0xff}
	unpaintedColor = color.RGBA{0x3a, 0x3a, 0x40, 0xff}
	labelColor     = color.RGBA{0xcc, 0xcc, 0xcc, 0xff}
)

type canvas struct {
	img  *image.RGBA
	px   float64
	ox   float64 // pixel offset of grid origin
	oy   float64
	rast vector.Rasterizer
}

// PNG renders s to PNG bytes.
func PNG(s *scene.Scene, cat *scene.Catalog, opt Options) ([]byte, error) {
	img := Image(s, cat, opt)
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Image(s *scene.Scene, cat *scene.Catalog, opt Options) *image.RGBA {
	ext := s.Grid.MapExtent(s.Width, s.Height)
	px := opt.PxPerCell
	if px <= 0 {
		maxPx := float64(opt.MaxPx)
		if maxPx <= 0 {
			maxPx = 1600
		}
		px = math.Max(6, math.Min(48, maxPx/math.Max(ext.X, ext.Y)))
	}
	m := 0.0
	if opt.Labels {
		m = margin
	}
	w, h := int(math.Ceil(ext.X*px+2*m)), int(math.Ceil(ext.Y*px+2*m))
	c := &canvas{img: image.NewRGBA(image.Rect(0, 0, w, h)), px: px, ox: m, oy: m}
	draw.Draw(c.img, c.img.Bounds(), image.NewUniform(bgColor), image.Point{}, draw.Src)

	g := s.Grid
	// terrain
	for row := 0; row < s.Height; row++ {
		for col := 0; col < s.Width; col++ {
			cell := scene.Cell{Col: col, Row: row}
			fill := unpaintedColor
			if t := cat.Terrain(s.Terrain[cell.Key()]); t != nil {
				fill = parseHex(t.Color)
			}
			c.fillPoly(g.CellCorners(cell), fill)
		}
	}
	// grid lines
	if g.Visible && px >= 8 {
		rgb := parseHex(g.Color)
		gc := color.NRGBA{rgb.R, rgb.G, rgb.B, uint8(math.Max(40, g.Opacity*255))}
		for row := 0; row < s.Height; row++ {
			for col := 0; col < s.Width; col++ {
				c.strokePoly(g.CellCorners(scene.Cell{Col: col, Row: row}), true, math.Max(1, px/40), gc)
			}
		}
	}
	// floor objects, walls, overhead objects, tokens
	objs := make([]*scene.Object, 0, len(s.Objects))
	for _, o := range s.Objects {
		objs = append(objs, o)
	}
	slices.SortFunc(objs, func(a, b *scene.Object) int {
		if a.Layer != b.Layer {
			if a.Layer == scene.LayerFloor {
				return -1
			}
			return 1
		}
		if a.Z != b.Z {
			return a.Z - b.Z
		}
		return compareIDs(a.ID, b.ID)
	})
	drawObjects := func(layer string) {
		for _, o := range objs {
			if o.Layer == layer {
				c.drawObject(o, cat.Asset(o.AssetID))
			}
		}
	}
	drawObjects(scene.LayerFloor)
	for _, wl := range s.Walls {
		st := cat.WallStyle(wl.Style)
		col, width := color.RGBA{0x20, 0x20, 0x20, 0xff}, 0.25
		if st != nil {
			col, width = parseHex(st.Color), st.Width
		}
		c.strokePoly(wl.Points, wl.Closed, math.Max(2, width*px), col)
	}
	drawObjects(scene.LayerOverhead)
	for _, t := range s.Tokens {
		ctr := g.CellCenter(t.Cell)
		// multi-cell tokens extend right/down from their anchor cell
		off := float64(t.Size-1) / 2
		ctr.X += off
		ctr.Y += off
		r := 0.42 * float64(t.Size)
		c.fillCircle(ctr, r+0.05, color.RGBA{0xff, 0xff, 0xff, 0xff})
		c.fillCircle(ctr, r, parseHex(t.Color))
		if t.Name != "" {
			c.label(ctr, t.Name[:1], color.RGBA{0xff, 0xff, 0xff, 0xff})
		}
	}
	if opt.Labels {
		c.drawLabels(s)
	}
	return c.img
}

func compareIDs(a, b string) int {
	// ids are a prefix plus a counter; compare numerically when possible
	na, ea := strconv.Atoi(a[1:])
	nb, eb := strconv.Atoi(b[1:])
	if ea == nil && eb == nil {
		return na - nb
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (c *canvas) toPx(p scene.Point) (float64, float64) {
	return c.ox + p.X*c.px, c.oy + p.Y*c.px
}

// fillPoly rasterizes a polygon given in grid units, using a rasterizer sized
// to the polygon's bounding box so cost scales with the shape, not the image.
func (c *canvas) fillPoly(pts []scene.Point, col color.Color) {
	c.fillPxPoly(c.pxPoints(pts), col)
}

func (c *canvas) pxPoints(pts []scene.Point) [][2]float64 {
	out := make([][2]float64, len(pts))
	for i, p := range pts {
		x, y := c.toPx(p)
		out[i] = [2]float64{x, y}
	}
	return out
}

func (c *canvas) fillPxPoly(pts [][2]float64, col color.Color) {
	b := c.img.Bounds()
	pts = clipPoly(pts, float64(b.Min.X), float64(b.Min.Y), float64(b.Max.X), float64(b.Max.Y))
	if len(pts) < 3 {
		return
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
		maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
	}
	box := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))).Intersect(b)
	if box.Empty() {
		return
	}
	x0, y0 := float64(box.Min.X), float64(box.Min.Y)
	c.rast.Reset(box.Dx(), box.Dy())
	c.rast.DrawOp = draw.Over
	c.rast.MoveTo(float32(pts[0][0]-x0), float32(pts[0][1]-y0))
	for _, p := range pts[1:] {
		c.rast.LineTo(float32(p[0]-x0), float32(p[1]-y0))
	}
	c.rast.ClosePath()
	c.rast.Draw(c.img, box, image.NewUniform(col), image.Point{})
}

// clipPoly clips a polygon to an axis-aligned rectangle (Sutherland-Hodgman);
// the rasterizer does not clip on its own.
func clipPoly(pts [][2]float64, x0, y0, x1, y1 float64) [][2]float64 {
	edge := func(in [][2]float64, inside func(p [2]float64) bool, cross func(a, b [2]float64) [2]float64) [][2]float64 {
		var out [][2]float64
		for i, cur := range in {
			prev := in[(i+len(in)-1)%len(in)]
			if inside(cur) {
				if !inside(prev) {
					out = append(out, cross(prev, cur))
				}
				out = append(out, cur)
			} else if inside(prev) {
				out = append(out, cross(prev, cur))
			}
		}
		return out
	}
	atX := func(x float64) func(a, b [2]float64) [2]float64 {
		return func(a, b [2]float64) [2]float64 {
			t := (x - a[0]) / (b[0] - a[0])
			return [2]float64{x, a[1] + t*(b[1]-a[1])}
		}
	}
	atY := func(y float64) func(a, b [2]float64) [2]float64 {
		return func(a, b [2]float64) [2]float64 {
			t := (y - a[1]) / (b[1] - a[1])
			return [2]float64{a[0] + t*(b[0]-a[0]), y}
		}
	}
	pts = edge(pts, func(p [2]float64) bool { return p[0] >= x0 }, atX(x0))
	if len(pts) > 0 {
		pts = edge(pts, func(p [2]float64) bool { return p[0] <= x1 }, atX(x1))
	}
	if len(pts) > 0 {
		pts = edge(pts, func(p [2]float64) bool { return p[1] >= y0 }, atY(y0))
	}
	if len(pts) > 0 {
		pts = edge(pts, func(p [2]float64) bool { return p[1] <= y1 }, atY(y1))
	}
	return pts
}

// strokePoly draws a polyline of the given pixel width.
func (c *canvas) strokePoly(pts []scene.Point, closed bool, width float64, col color.Color) {
	pp := c.pxPoints(pts)
	if closed && len(pp) > 2 {
		pp = append(pp, pp[0])
	}
	hw := width / 2
	for i := 0; i+1 < len(pp); i++ {
		a, b := pp[i], pp[i+1]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*hw, dx/l*hw
		c.fillPxPoly([][2]float64{{a[0] + nx, a[1] + ny}, {b[0] + nx, b[1] + ny}, {b[0] - nx, b[1] - ny}, {a[0] - nx, a[1] - ny}}, col)
	}
	if width >= 4 {
		for _, p := range pp {
			c.fillPxPoly(circlePts(p[0], p[1], hw, 12), col)
		}
	}
}

func circlePts(x, y, r float64, n int) [][2]float64 {
	pts := make([][2]float64, n)
	for i := range pts {
		a := 2 * math.Pi * float64(i) / float64(n)
		pts[i] = [2]float64{x + r*math.Cos(a), y + r*math.Sin(a)}
	}
	return pts
}

func (c *canvas) fillCircle(ctr scene.Point, r float64, col color.RGBA) {
	x, y := c.toPx(ctr)
	c.fillPxPoly(circlePts(x, y, r*c.px, 24), col)
}

func (c *canvas) drawObject(o *scene.Object, a *scene.Asset) {
	size, col, shape := 1.0, color.RGBA{0xff, 0x00, 0xff, 0xff}, "circle"
	if a != nil {
		size, col, shape = a.Size, parseHex(a.Color), a.Shape
	}
	half := size * o.Scale * 0.45
	edge := darken(col)
	if shape == "rect" {
		rad := o.Rotation * math.Pi / 180
		cs, sn := math.Cos(rad), math.Sin(rad)
		corner := func(dx, dy float64) scene.Point {
			return scene.Point{X: o.X + dx*cs - dy*sn, Y: o.Y + dx*sn + dy*cs}
		}
		outer := []scene.Point{corner(-half, -half), corner(half, -half), corner(half, half), corner(-half, half)}
		in := half - 0.06
		inner := []scene.Point{corner(-in, -in), corner(in, -in), corner(in, in), corner(-in, in)}
		c.fillPoly(outer, edge)
		c.fillPoly(inner, col)
		return
	}
	c.fillCircle(scene.Point{X: o.X, Y: o.Y}, half, edge)
	c.fillCircle(scene.Point{X: o.X, Y: o.Y}, math.Max(0.01, half-0.06), col)
}

func (c *canvas) label(p scene.Point, s string, col color.RGBA) {
	x, y := c.toPx(p)
	c.text(x, y, s, col)
}

func (c *canvas) text(cx, cy float64, s string, col color.RGBA) {
	d := font.Drawer{Dst: c.img, Src: image.NewUniform(col), Face: basicfont.Face7x13}
	w := d.MeasureString(s).Round()
	d.Dot = fixed.P(int(cx)-w/2, int(cy)+4)
	d.DrawString(s)
}

// drawLabels writes column numbers above and below the map and row numbers on
// both sides, every 1, 2, 5 or 10 cells depending on zoom.
func (c *canvas) drawLabels(s *scene.Scene) {
	step := 1
	for _, st := range []int{1, 2, 5, 10} {
		step = st
		if float64(st)*c.px >= 22 {
			break
		}
	}
	g := s.Grid
	h := float64(c.img.Bounds().Dy())
	w := float64(c.img.Bounds().Dx())
	for col := 0; col < s.Width; col += step {
		x, _ := c.toPx(g.CellCenter(scene.Cell{Col: col, Row: 0}))
		c.text(x, margin/2, strconv.Itoa(col), labelColor)
		c.text(x, h-margin/2, strconv.Itoa(col), labelColor)
	}
	for row := 0; row < s.Height; row += step {
		_, y := c.toPx(g.CellCenter(scene.Cell{Col: 0, Row: row}))
		c.text(margin/2, y, strconv.Itoa(row), labelColor)
		c.text(w-margin/2, y, strconv.Itoa(row), labelColor)
	}
}

func parseHex(s string) color.RGBA {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{0xff, 0x00, 0xff, 0xff}
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{0xff, 0x00, 0xff, 0xff}
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

func darken(c color.RGBA) color.RGBA {
	return color.RGBA{uint8(float64(c.R) * 0.55), uint8(float64(c.G) * 0.55), uint8(float64(c.B) * 0.55), c.A}
}
