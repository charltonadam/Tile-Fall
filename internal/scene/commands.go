package scene

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
)

// Command is a validated, JSON-serializable change request. Every mutation of
// a scene, whether it comes from a browser, MCP or REST, is a Command applied
// through Table.Apply. The JSON form is {"type": "<Type()>", ...fields}.
type Command interface {
	Type() string
	// plan validates the command against the current scene and returns the
	// patch that implements it plus a result for the caller (new ids etc).
	// It must not modify s except through s.newID.
	plan(s *Scene, cat *Catalog) (*Patch, any, error)
}

var registry = map[string]func() Command{}

func register(f func() Command) { registry[f().Type()] = f }

func init() {
	register(func() Command { return &UpdateScene{} })
	register(func() Command { return &FillTerrain{} })
	register(func() Command { return &PaintPath{} })
	register(func() Command { return &PlaceObjects{} })
	register(func() Command { return &UpdateObject{} })
	register(func() Command { return &RemoveObjects{} })
	register(func() Command { return &ScatterObjects{} })
	register(func() Command { return &AddWalls{} })
	register(func() Command { return &RemoveWalls{} })
	register(func() Command { return &AddToken{} })
	register(func() Command { return &MoveToken{} })
	register(func() Command { return &UpdateToken{} })
	register(func() Command { return &RemoveTokens{} })
	register(func() Command { return &ClearRegion{} })
}

// DecodeCommand parses {"type": ..., ...} into the matching Command.
func DecodeCommand(data []byte) (Command, error) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	f, ok := registry[env.Type]
	if !ok {
		return nil, fmt.Errorf("unknown command type %q", env.Type)
	}
	c := f()
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", env.Type, err)
	}
	return c, nil
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func checkColor(c string) error {
	if !colorRe.MatchString(c) {
		return fmt.Errorf("color %q must look like #rrggbb", c)
	}
	return nil
}

// ---- scene settings ----

type UpdateScene struct {
	Name            *string     `json:"name,omitempty"`
	Width           *int        `json:"width,omitempty" jsonschema:"new width in cells; terrain outside the new bounds is removed"`
	Height          *int        `json:"height,omitempty" jsonschema:"new height in cells"`
	Grid            *GridUpdate `json:"grid,omitempty"`
	Background      *Background `json:"background,omitempty" jsonschema:"background image placed in grid units"`
	ClearBackground bool        `json:"clearBackground,omitempty"`
}

type GridUpdate struct {
	Type           *GridType       `json:"type,omitempty" jsonschema:"square or hex"`
	HexOrientation *HexOrientation `json:"hexOrientation,omitempty" jsonschema:"pointy or flat"`
	CellSize       *int            `json:"cellSize,omitempty" jsonschema:"pixels per cell at 100% zoom"`
	Visible        *bool           `json:"visible,omitempty"`
	Color          *string         `json:"color,omitempty"`
	Opacity        *float64        `json:"opacity,omitempty"`
	Distance       *float64        `json:"distance,omitempty" jsonschema:"distance one cell represents (D&D default 5)"`
	Units          *string         `json:"units,omitempty" jsonschema:"distance unit label (default ft)"`
}

func (*UpdateScene) Type() string { return "update_scene" }

func (c *UpdateScene) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	m := s.Meta
	if c.Name != nil {
		m.Name = *c.Name
	}
	if c.Width != nil {
		m.Width = *c.Width
	}
	if c.Height != nil {
		m.Height = *c.Height
	}
	if g := c.Grid; g != nil {
		if g.Type != nil {
			m.Grid.Type = *g.Type
		}
		if g.HexOrientation != nil {
			m.Grid.HexOrientation = *g.HexOrientation
		}
		if g.CellSize != nil {
			m.Grid.CellSize = *g.CellSize
		}
		if g.Visible != nil {
			m.Grid.Visible = *g.Visible
		}
		if g.Color != nil {
			if err := checkColor(*g.Color); err != nil {
				return nil, nil, err
			}
			m.Grid.Color = *g.Color
		}
		if g.Opacity != nil {
			m.Grid.Opacity = *g.Opacity
		}
		if g.Distance != nil {
			m.Grid.Distance = *g.Distance
		}
		if g.Units != nil {
			m.Grid.Units = *g.Units
		}
	}
	if c.Background != nil {
		bg := *c.Background
		if bg.URL == "" || bg.Width <= 0 || bg.Height <= 0 {
			return nil, nil, errors.New("background needs a url and positive width/height")
		}
		m.Background = &bg
	}
	if c.ClearBackground {
		m.Background = nil
	}
	if m.Grid.Type == GridHex && m.Grid.HexOrientation == "" {
		m.Grid.HexOrientation = HexPointy
	}
	if err := m.Validate(); err != nil {
		return nil, nil, err
	}
	p := &Patch{Meta: &m}
	if m.Width < s.Width || m.Height < s.Height {
		for k := range s.Terrain {
			if cell, _ := ParseCellKey(k); cell.Col >= m.Width || cell.Row >= m.Height {
				p.setTerrain(k, "")
			}
		}
		for id, t := range s.Tokens {
			if t.Cell.Col >= m.Width || t.Cell.Row >= m.Height {
				t2 := *t
				t2.Cell = Cell{min(t.Cell.Col, m.Width-1), min(t.Cell.Row, m.Height-1)}
				p.setToken(id, &t2)
			}
		}
	}
	return p, nil, nil
}

// ---- terrain ----

type FillTerrain struct {
	Region  Region `json:"region"`
	Terrain string `json:"terrain" jsonschema:"terrain id from the catalog, or empty string to erase"`
}

func (*FillTerrain) Type() string { return "fill_terrain" }

func (c *FillTerrain) plan(s *Scene, cat *Catalog) (*Patch, any, error) {
	if c.Terrain != "" && cat.Terrain(c.Terrain) == nil {
		return nil, nil, fmt.Errorf("unknown terrain %q", c.Terrain)
	}
	cells, err := c.Region.Resolve(s)
	if err != nil {
		return nil, nil, err
	}
	p := &Patch{}
	for _, cell := range cells {
		p.setTerrain(cell.Key(), c.Terrain)
	}
	return p, nil, nil
}

type PaintPath struct {
	Points  []Cell  `json:"points" jsonschema:"cells the path passes through, in order; the path runs between their centers"`
	Width   float64 `json:"width,omitempty" jsonschema:"path width in cells (default 1)"`
	Terrain string  `json:"terrain" jsonschema:"terrain id, or empty string to erase"`
}

func (*PaintPath) Type() string { return "paint_path" }

func (c *PaintPath) plan(s *Scene, cat *Catalog) (*Patch, any, error) {
	if c.Terrain != "" && cat.Terrain(c.Terrain) == nil {
		return nil, nil, fmt.Errorf("unknown terrain %q", c.Terrain)
	}
	if len(c.Points) == 0 {
		return nil, nil, errors.New("path needs at least one point")
	}
	w := c.Width
	if w == 0 {
		w = 1
	}
	if w < 0 || w > 50 {
		return nil, nil, errors.New("width must be between 0 and 50")
	}
	pts := make([]Point, len(c.Points))
	for i, cell := range c.Points {
		pts[i] = s.Grid.CellCenter(cell)
	}
	p := &Patch{}
	for _, cell := range s.filter(func(cell Cell) bool {
		return distToPolyline(s.Grid.CellCenter(cell), pts, false) <= w/2+1e-6
	}) {
		p.setTerrain(cell.Key(), c.Terrain)
	}
	return p, nil, nil
}

// ---- objects ----

type ObjectSpec struct {
	AssetID  string   `json:"assetId" jsonschema:"asset id from the catalog"`
	Cell     *Cell    `json:"cell,omitempty" jsonschema:"place centered on this cell (use this or x/y)"`
	X        *float64 `json:"x,omitempty" jsonschema:"center x in grid units"`
	Y        *float64 `json:"y,omitempty" jsonschema:"center y in grid units"`
	Rotation float64  `json:"rotation,omitempty" jsonschema:"degrees clockwise"`
	Scale    float64  `json:"scale,omitempty" jsonschema:"size multiplier (default 1)"`
	Layer    string   `json:"layer,omitempty" jsonschema:"floor or overhead (default: the asset's layer)"`
	Z        int      `json:"z,omitempty" jsonschema:"draw order within the layer"`
}

type PlaceObjects struct {
	Objects []ObjectSpec `json:"objects"`
}

type IDsResult struct {
	IDs []string `json:"ids"`
}

func (*PlaceObjects) Type() string { return "place_objects" }

func (c *PlaceObjects) plan(s *Scene, cat *Catalog) (*Patch, any, error) {
	if len(c.Objects) == 0 {
		return nil, nil, errors.New("no objects given")
	}
	p := &Patch{}
	res := IDsResult{IDs: []string{}}
	for i, spec := range c.Objects {
		a := cat.Asset(spec.AssetID)
		if a == nil {
			return nil, nil, fmt.Errorf("objects[%d]: unknown asset %q", i, spec.AssetID)
		}
		var pos Point
		switch {
		case spec.Cell != nil:
			pos = s.Grid.CellCenter(*spec.Cell)
		case spec.X != nil && spec.Y != nil:
			pos = Point{*spec.X, *spec.Y}
		default:
			return nil, nil, fmt.Errorf("objects[%d]: give cell or x and y", i)
		}
		o := &Object{AssetID: a.ID, X: pos.X, Y: pos.Y, Rotation: spec.Rotation, Scale: spec.Scale, Layer: spec.Layer, Z: spec.Z}
		if o.Scale == 0 {
			o.Scale = 1
		}
		if o.Layer == "" {
			o.Layer = a.Layer
		}
		if err := checkObject(o); err != nil {
			return nil, nil, fmt.Errorf("objects[%d]: %w", i, err)
		}
		o.ID = s.newID("o")
		p.setObject(o.ID, o)
		res.IDs = append(res.IDs, o.ID)
	}
	return p, res, nil
}

func checkObject(o *Object) error {
	if o.Scale <= 0 || o.Scale > 20 {
		return errors.New("scale must be in (0, 20]")
	}
	if o.Layer != LayerFloor && o.Layer != LayerOverhead {
		return fmt.Errorf("layer must be %q or %q", LayerFloor, LayerOverhead)
	}
	o.Rotation = math.Mod(math.Mod(o.Rotation, 360)+360, 360)
	return nil
}

type UpdateObject struct {
	ID       string   `json:"id"`
	X        *float64 `json:"x,omitempty"`
	Y        *float64 `json:"y,omitempty"`
	Cell     *Cell    `json:"cell,omitempty" jsonschema:"move to the center of this cell"`
	Rotation *float64 `json:"rotation,omitempty"`
	Scale    *float64 `json:"scale,omitempty"`
	Layer    *string  `json:"layer,omitempty"`
	Z        *int     `json:"z,omitempty"`
}

func (*UpdateObject) Type() string { return "update_object" }

func (c *UpdateObject) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	old := s.Objects[c.ID]
	if old == nil {
		return nil, nil, fmt.Errorf("no object %q", c.ID)
	}
	o := *old
	if c.Cell != nil {
		pos := s.Grid.CellCenter(*c.Cell)
		o.X, o.Y = pos.X, pos.Y
	}
	if c.X != nil {
		o.X = *c.X
	}
	if c.Y != nil {
		o.Y = *c.Y
	}
	if c.Rotation != nil {
		o.Rotation = *c.Rotation
	}
	if c.Scale != nil {
		o.Scale = *c.Scale
	}
	if c.Layer != nil {
		o.Layer = *c.Layer
	}
	if c.Z != nil {
		o.Z = *c.Z
	}
	if err := checkObject(&o); err != nil {
		return nil, nil, err
	}
	p := &Patch{}
	p.setObject(o.ID, &o)
	return p, nil, nil
}

type RemoveObjects struct {
	IDs []string `json:"ids"`
}

func (*RemoveObjects) Type() string { return "remove_objects" }

func (c *RemoveObjects) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	p := &Patch{}
	for _, id := range c.IDs {
		if s.Objects[id] == nil {
			return nil, nil, fmt.Errorf("no object %q", id)
		}
		p.setObject(id, nil)
	}
	return p, nil, nil
}

type ScatterObjects struct {
	Region        Region   `json:"region" jsonschema:"where to scatter"`
	AssetIDs      []string `json:"assetIds" jsonschema:"assets to choose from at random"`
	Count         int      `json:"count,omitempty" jsonschema:"number of objects to place (or use density)"`
	Density       float64  `json:"density,omitempty" jsonschema:"objects per cell, e.g. 0.2; used when count is 0"`
	Seed          int64    `json:"seed,omitempty" jsonschema:"random seed; same inputs and seed give the same result"`
	MinSpacing    float64  `json:"minSpacing,omitempty" jsonschema:"minimum distance in cells between scattered objects and existing objects on the same layer"`
	OnTerrain     []string `json:"onTerrain,omitempty" jsonschema:"only place on cells painted with one of these terrains"`
	FixedRotation bool     `json:"fixedRotation,omitempty" jsonschema:"keep rotation 0 instead of random"`
	ScaleMin      float64  `json:"scaleMin,omitempty" jsonschema:"minimum random scale (default 0.8)"`
	ScaleMax      float64  `json:"scaleMax,omitempty" jsonschema:"maximum random scale (default 1.2)"`
	Layer         string   `json:"layer,omitempty" jsonschema:"floor or overhead (default: each asset's layer)"`
}

type ScatterResult struct {
	IDs       []string `json:"ids"`
	Requested int      `json:"requested"`
	Placed    int      `json:"placed"`
}

func (*ScatterObjects) Type() string { return "scatter_objects" }

func (c *ScatterObjects) plan(s *Scene, cat *Catalog) (*Patch, any, error) {
	if len(c.AssetIDs) == 0 {
		return nil, nil, errors.New("assetIds is empty")
	}
	assets := make([]*Asset, len(c.AssetIDs))
	for i, id := range c.AssetIDs {
		if assets[i] = cat.Asset(id); assets[i] == nil {
			return nil, nil, fmt.Errorf("unknown asset %q", id)
		}
	}
	cells, err := c.Region.Resolve(s)
	if err != nil {
		return nil, nil, err
	}
	if len(c.OnTerrain) > 0 {
		cells = slices.DeleteFunc(cells, func(cell Cell) bool { return !slices.Contains(c.OnTerrain, s.Terrain[cell.Key()]) })
	}
	want := c.Count
	if want == 0 {
		want = int(math.Round(c.Density * float64(len(cells))))
	}
	if want < 0 || want > 2000 {
		return nil, nil, errors.New("count must be between 0 and 2000")
	}
	smin, smax := c.ScaleMin, c.ScaleMax
	if smin == 0 {
		smin = 0.8
	}
	if smax == 0 {
		smax = max(1.2, smin)
	}
	if smin > smax {
		return nil, nil, errors.New("scaleMin > scaleMax")
	}

	rng := rand.New(rand.NewPCG(uint64(c.Seed), 0x7469_6c65_6661_6c6c))
	var placed []*Object
	tooClose := func(pt Point, layer string) bool {
		if c.MinSpacing <= 0 {
			return false
		}
		for _, o := range s.Objects {
			if o.Layer == layer && dist(pt, Point{o.X, o.Y}) < c.MinSpacing {
				return true
			}
		}
		for _, o := range placed {
			if o.Layer == layer && dist(pt, Point{o.X, o.Y}) < c.MinSpacing {
				return true
			}
		}
		return false
	}
	p := &Patch{}
	res := ScatterResult{IDs: []string{}, Requested: want}
	for attempt := 0; len(cells) > 0 && len(placed) < want && attempt < want*30; attempt++ {
		cell := cells[rng.IntN(len(cells))]
		ctr := s.Grid.CellCenter(cell)
		pt := Point{ctr.X + (rng.Float64()-0.5)*0.8, ctr.Y + (rng.Float64()-0.5)*0.8}
		a := assets[rng.IntN(len(assets))]
		layer := c.Layer
		if layer == "" {
			layer = a.Layer
		}
		if tooClose(pt, layer) {
			continue
		}
		o := &Object{AssetID: a.ID, X: pt.X, Y: pt.Y, Scale: smin + rng.Float64()*(smax-smin), Layer: layer}
		if !c.FixedRotation {
			o.Rotation = math.Round(rng.Float64() * 360)
		}
		if err := checkObject(o); err != nil {
			return nil, nil, err
		}
		o.ID = s.newID("o")
		placed = append(placed, o)
		p.setObject(o.ID, o)
		res.IDs = append(res.IDs, o.ID)
	}
	res.Placed = len(placed)
	return p, res, nil
}

// ---- walls ----

type WallSpec struct {
	Points []Point   `json:"points,omitempty" jsonschema:"polyline vertices in grid units; on square grids integer coordinates are cell corners"`
	Rect   *CellRect `json:"rect,omitempty" jsonschema:"outline the outer edge of this rectangle of cells (instead of points)"`
	Closed bool      `json:"closed,omitempty" jsonschema:"connect the last point back to the first"`
	Style  string    `json:"style,omitempty" jsonschema:"wall style id from the catalog (default stone)"`
}

type AddWalls struct {
	Walls []WallSpec `json:"walls"`
}

func (*AddWalls) Type() string { return "add_walls" }

func (c *AddWalls) plan(s *Scene, cat *Catalog) (*Patch, any, error) {
	if len(c.Walls) == 0 {
		return nil, nil, errors.New("no walls given")
	}
	p := &Patch{}
	res := IDsResult{IDs: []string{}}
	for i, spec := range c.Walls {
		w := &Wall{Points: spec.Points, Closed: spec.Closed, Style: spec.Style}
		if w.Style == "" {
			w.Style = "stone"
		}
		if cat.WallStyle(w.Style) == nil {
			return nil, nil, fmt.Errorf("walls[%d]: unknown style %q", i, w.Style)
		}
		if spec.Rect != nil {
			w.Points, w.Closed = rectOutline(s.Grid, *spec.Rect), true
		}
		if len(w.Points) < 2 {
			return nil, nil, fmt.Errorf("walls[%d]: needs at least 2 points or a rect", i)
		}
		w.ID = s.newID("w")
		p.setWall(w.ID, w)
		res.IDs = append(res.IDs, w.ID)
	}
	return p, res, nil
}

// rectOutline returns the bounding box of the corners of a cell rectangle.
func rectOutline(g Grid, r CellRect) []Point {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for col := min(r.From.Col, r.To.Col); col <= max(r.From.Col, r.To.Col); col++ {
		for row := min(r.From.Row, r.To.Row); row <= max(r.From.Row, r.To.Row); row++ {
			for _, pt := range g.CellCorners(Cell{col, row}) {
				minX, minY = math.Min(minX, pt.X), math.Min(minY, pt.Y)
				maxX, maxY = math.Max(maxX, pt.X), math.Max(maxY, pt.Y)
			}
		}
	}
	return []Point{{minX, minY}, {maxX, minY}, {maxX, maxY}, {minX, maxY}}
}

type RemoveWalls struct {
	IDs []string `json:"ids"`
}

func (*RemoveWalls) Type() string { return "remove_walls" }

func (c *RemoveWalls) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	p := &Patch{}
	for _, id := range c.IDs {
		if s.Walls[id] == nil {
			return nil, nil, fmt.Errorf("no wall %q", id)
		}
		p.setWall(id, nil)
	}
	return p, nil, nil
}

// ---- tokens ----

type AddToken struct {
	TemplateID string `json:"templateId,omitempty" jsonschema:"token library template to copy name, size, color, image and maxHp from; fields given here override it"`
	Name       string `json:"name,omitempty" jsonschema:"required unless templateId is given"`
	Cell       Cell   `json:"cell"`
	Size       int    `json:"size,omitempty" jsonschema:"footprint in cells: 1 medium (default), 2 large, 3 huge, 4 gargantuan"`
	Color      string `json:"color,omitempty" jsonschema:"#rrggbb (default #c0392b)"`
	ImageURL   string `json:"imageUrl,omitempty"`
	MaxHP      int    `json:"maxHp,omitempty" jsonschema:"maximum hit points; the token starts at full hp (0 = not tracked)"`
}

type IDResult struct {
	ID string `json:"id"`
}

func (*AddToken) Type() string { return "add_token" }

// FromTemplate fills the fields left empty from tpl. game resolves templateId
// with it before the command reaches the Table, because the library is not
// part of the scene.
func (c *AddToken) FromTemplate(tpl *TokenTemplate) {
	c.TemplateID = tpl.ID
	if c.Name == "" {
		c.Name = tpl.Name
	}
	if c.Size == 0 {
		c.Size = tpl.Size
	}
	if c.Color == "" {
		c.Color = tpl.Color
	}
	if c.ImageURL == "" {
		c.ImageURL = tpl.ImageURL
	}
	if c.MaxHP == 0 {
		c.MaxHP = tpl.MaxHP
	}
}

func (c *AddToken) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	t := &Token{Name: c.Name, Cell: c.Cell, Size: c.Size, Color: c.Color, ImageURL: c.ImageURL, MaxHP: c.MaxHP, HP: c.MaxHP, TemplateID: c.TemplateID}
	if t.Size == 0 {
		t.Size = 1
	}
	if t.Color == "" {
		t.Color = "#c0392b"
	}
	if err := checkToken(s, t); err != nil {
		return nil, nil, err
	}
	if t.TemplateID != "" {
		t.Name = uniqueName(s, t.Name)
	}
	t.ID = s.newID("t")
	p := &Patch{}
	p.setToken(t.ID, t)
	return p, IDResult{t.ID}, nil
}

// uniqueName numbers repeated spawns of a template: Goblin, Goblin 2, ...
// Commands in a batch see earlier ones, so a batch numbers correctly too.
func uniqueName(s *Scene, name string) string {
	taken := map[string]bool{}
	for _, t := range s.Tokens {
		taken[t.Name] = true
	}
	if !taken[name] {
		return name
	}
	for n := 2; ; n++ {
		if c := name + " " + strconv.Itoa(n); !taken[c] {
			return c
		}
	}
}

// checkToken validates t and clamps its hp into [0, maxHp].
func checkToken(s *Scene, t *Token) error {
	if t.Name == "" {
		return errors.New("token needs a name")
	}
	if !s.InBounds(t.Cell) {
		return fmt.Errorf("cell (%d,%d) is outside the %dx%d map", t.Cell.Col, t.Cell.Row, s.Width, s.Height)
	}
	if t.Size < 1 || t.Size > 6 {
		return errors.New("size must be between 1 and 6")
	}
	if t.MaxHP < 0 || t.MaxHP > maxHP {
		return fmt.Errorf("maxHp must be between 0 and %d", maxHP)
	}
	t.HP = max(0, min(t.HP, t.MaxHP))
	return checkColor(t.Color)
}

type MoveToken struct {
	ID   string `json:"id"`
	Cell Cell   `json:"cell"`
}

func (*MoveToken) Type() string { return "move_token" }

func (c *MoveToken) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	old := s.Tokens[c.ID]
	if old == nil {
		return nil, nil, fmt.Errorf("no token %q", c.ID)
	}
	if !s.InBounds(c.Cell) {
		return nil, nil, fmt.Errorf("cell (%d,%d) is outside the %dx%d map", c.Cell.Col, c.Cell.Row, s.Width, s.Height)
	}
	t := *old
	t.Cell = c.Cell
	p := &Patch{}
	p.setToken(t.ID, &t)
	return p, nil, nil
}

// UpdateToken changes one token on the map. It never touches the library
// template the token came from.
type UpdateToken struct {
	ID       string  `json:"id"`
	Name     *string `json:"name,omitempty"`
	Size     *int    `json:"size,omitempty"`
	Color    *string `json:"color,omitempty"`
	ImageURL *string `json:"imageUrl,omitempty"`
	MaxHP    *int    `json:"maxHp,omitempty" jsonschema:"maximum hit points (0 = not tracked)"`
	HP       *int    `json:"hp,omitempty" jsonschema:"set current hit points; clamped to 0..maxHp"`
	HPDelta  *int    `json:"hpDelta,omitempty" jsonschema:"add to current hit points after hp is applied: negative for damage, positive for healing; clamped to 0..maxHp"`
}

func (*UpdateToken) Type() string { return "update_token" }

func (c *UpdateToken) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	old := s.Tokens[c.ID]
	if old == nil {
		return nil, nil, fmt.Errorf("no token %q", c.ID)
	}
	t := *old
	if c.Name != nil {
		t.Name = *c.Name
	}
	if c.Size != nil {
		t.Size = *c.Size
	}
	if c.Color != nil {
		t.Color = *c.Color
	}
	if c.ImageURL != nil {
		t.ImageURL = *c.ImageURL
	}
	if c.MaxHP != nil {
		t.MaxHP = *c.MaxHP
	}
	if c.HP != nil {
		t.HP = *c.HP
	}
	if c.HPDelta != nil {
		t.HP += *c.HPDelta
	}
	if err := checkToken(s, &t); err != nil {
		return nil, nil, err
	}
	p := &Patch{}
	p.setToken(t.ID, &t)
	return p, nil, nil
}

type RemoveTokens struct {
	IDs []string `json:"ids"`
}

func (*RemoveTokens) Type() string { return "remove_tokens" }

func (c *RemoveTokens) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	p := &Patch{}
	for _, id := range c.IDs {
		if s.Tokens[id] == nil {
			return nil, nil, fmt.Errorf("no token %q", id)
		}
		p.setToken(id, nil)
	}
	return p, nil, nil
}

// ---- bulk ----

type ClearRegion struct {
	Region  Region `json:"region"`
	Terrain bool   `json:"terrain,omitempty" jsonschema:"erase terrain in the region"`
	Objects bool   `json:"objects,omitempty" jsonschema:"remove objects whose center is in the region"`
	Walls   bool   `json:"walls,omitempty" jsonschema:"remove walls with any vertex in the region"`
	Tokens  bool   `json:"tokens,omitempty" jsonschema:"remove tokens in the region"`
}

func (*ClearRegion) Type() string { return "clear_region" }

func (c *ClearRegion) plan(s *Scene, _ *Catalog) (*Patch, any, error) {
	if !c.Terrain && !c.Objects && !c.Walls && !c.Tokens {
		return nil, nil, errors.New("set at least one of terrain, objects, walls, tokens")
	}
	cells, err := c.Region.Resolve(s)
	if err != nil {
		return nil, nil, err
	}
	in := make(map[Cell]bool, len(cells))
	for _, cell := range cells {
		in[cell] = true
	}
	p := &Patch{}
	if c.Terrain {
		for _, cell := range cells {
			if _, ok := s.Terrain[cell.Key()]; ok {
				p.setTerrain(cell.Key(), "")
			}
		}
	}
	if c.Objects {
		for id, o := range s.Objects {
			if in[s.Grid.PointToCell(Point{o.X, o.Y})] {
				p.setObject(id, nil)
			}
		}
	}
	if c.Walls {
		for id, w := range s.Walls {
			for _, pt := range w.Points {
				if in[s.Grid.PointToCell(pt)] {
					p.setWall(id, nil)
					break
				}
			}
		}
	}
	if c.Tokens {
		for id, t := range s.Tokens {
			if in[t.Cell] {
				p.setToken(id, nil)
			}
		}
	}
	return p, nil, nil
}
