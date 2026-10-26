package scene

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
)

// Scene is one battlemap. It is a plain JSON document so agents can read,
// generate and diff it.
//
// Invariant: values stored in the maps are never mutated in place. Commands
// build new copies and put them in a Patch, so patches (and the undo stack,
// which holds inverse patches) can share pointers safely.
type Scene struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	NextID  int    `json:"nextId"`
	Meta
	// Terrain maps a cell key ("col,row") to a terrain id from the catalog.
	// Unpainted cells have no entry.
	Terrain map[string]string  `json:"terrain"`
	Objects map[string]*Object `json:"objects"`
	Walls   map[string]*Wall   `json:"walls"`
	Tokens  map[string]*Token  `json:"tokens"`
}

// Meta is the scene's settings; a Patch replaces it as a whole.
type Meta struct {
	Name       string      `json:"name"`
	Width      int         `json:"width" jsonschema:"map width in cells"`
	Height     int         `json:"height" jsonschema:"map height in cells"`
	Grid       Grid        `json:"grid"`
	Background *Background `json:"background"`
}

// Background is an image placed under the terrain, in grid units.
type Background struct {
	URL    string  `json:"url"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

const (
	LayerFloor    = "floor"
	LayerOverhead = "overhead"
)

// Object is a placed visual asset (tree, crate, pillar...). Position is the
// asset's center in grid units; it does not need to sit on the grid.
type Object struct {
	ID       string  `json:"id"`
	AssetID  string  `json:"assetId"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"` // degrees clockwise
	Scale    float64 `json:"scale"`    // multiplier on the asset's default size
	Layer    string  `json:"layer"`    // floor (under tokens) or overhead (over tokens)
	Z        int     `json:"z"`        // ordering within a layer
}

// Wall is a polyline in grid units. On a square grid integer coordinates are
// cell corners, so (0,0)-(5,0) runs along the top edge of row 0.
type Wall struct {
	ID     string  `json:"id"`
	Points []Point `json:"points"`
	Closed bool    `json:"closed"`
	Style  string  `json:"style"`
}

// Token is one creature on the map. A token spawned from a library template
// copies the template's fields; after that the two are independent, so
// editing one never changes the other. TemplateID only records provenance.
type Token struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Cell       Cell   `json:"cell"`
	Size       int    `json:"size"` // footprint in cells: 1 medium, 2 large, ...
	Color      string `json:"color"`
	ImageURL   string `json:"imageUrl,omitempty"`
	HP         int    `json:"hp"`
	MaxHP      int    `json:"maxHp"` // 0 means hit points are not tracked
	TemplateID string `json:"templateId,omitempty"`
}

// TokenTemplate is a token library entry: the defaults new tokens are created
// with. It has no current HP because a spawned token always starts at full HP.
type TokenTemplate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int    `json:"size"`
	Color    string `json:"color"`
	ImageURL string `json:"imageUrl,omitempty"`
	MaxHP    int    `json:"maxHp"`
}

const maxHP = 100000

func (t *TokenTemplate) Validate() error {
	if t.Name == "" {
		return errors.New("template needs a name")
	}
	if t.Size < 1 || t.Size > 6 {
		return errors.New("size must be between 1 and 6")
	}
	if t.MaxHP < 0 || t.MaxHP > maxHP {
		return fmt.Errorf("maxHp must be between 0 and %d", maxHP)
	}
	return checkColor(t.Color)
}

func New(id string, m Meta) *Scene {
	return &Scene{
		ID:      id,
		Meta:    m,
		Terrain: map[string]string{},
		Objects: map[string]*Object{},
		Walls:   map[string]*Wall{},
		Tokens:  map[string]*Token{},
	}
}

func (m Meta) Validate() error {
	if m.Width < 1 || m.Height < 1 || m.Width > 200 || m.Height > 200 {
		return fmt.Errorf("width and height must be between 1 and 200 cells")
	}
	return m.Grid.Validate()
}

func (s *Scene) InBounds(c Cell) bool {
	return c.Col >= 0 && c.Row >= 0 && c.Col < s.Width && c.Row < s.Height
}

func (s *Scene) newID(prefix string) string {
	s.NextID++
	return prefix + strconv.Itoa(s.NextID)
}

// Decode parses a scene, filling in nil maps.
func Decode(data []byte) (*Scene, error) {
	s := New("", Meta{})
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Terrain == nil {
		s.Terrain = map[string]string{}
	}
	if s.Objects == nil {
		s.Objects = map[string]*Object{}
	}
	if s.Walls == nil {
		s.Walls = map[string]*Wall{}
	}
	if s.Tokens == nil {
		s.Tokens = map[string]*Token{}
	}
	return s, nil
}

// Patch is the unit of change that is applied to a scene and broadcast to
// clients. Map entries set a value; a nil value (JSON null, or "" for terrain)
// deletes it. Clients only need to know how to apply patches, so the logic of
// each command lives only in Go.
type Patch struct {
	Version uint64             `json:"version"`
	Meta    *Meta              `json:"meta,omitempty"`
	Terrain map[string]string  `json:"terrain,omitempty"`
	Objects map[string]*Object `json:"objects,omitempty"`
	Walls   map[string]*Wall   `json:"walls,omitempty"`
	Tokens  map[string]*Token  `json:"tokens,omitempty"`
}

func (p *Patch) Empty() bool {
	return p.Meta == nil && len(p.Terrain) == 0 && len(p.Objects) == 0 && len(p.Walls) == 0 && len(p.Tokens) == 0
}

func (p *Patch) setTerrain(k, v string) {
	if p.Terrain == nil {
		p.Terrain = map[string]string{}
	}
	p.Terrain[k] = v
}

func (p *Patch) setObject(id string, o *Object) {
	if p.Objects == nil {
		p.Objects = map[string]*Object{}
	}
	p.Objects[id] = o
}

func (p *Patch) setWall(id string, w *Wall) {
	if p.Walls == nil {
		p.Walls = map[string]*Wall{}
	}
	p.Walls[id] = w
}

func (p *Patch) setToken(id string, t *Token) {
	if p.Tokens == nil {
		p.Tokens = map[string]*Token{}
	}
	p.Tokens[id] = t
}

// apply mutates the scene and returns the patch that would undo it. No-op
// entries (setting a value that is already there) are dropped from p.
func (s *Scene) apply(p *Patch) *Patch {
	inv := &Patch{}
	if p.Meta != nil {
		old := s.Meta
		inv.Meta = &old
		s.Meta = *p.Meta
	}
	for k, v := range p.Terrain {
		old := s.Terrain[k]
		if old == v {
			delete(p.Terrain, k)
			continue
		}
		inv.setTerrain(k, old)
		if v == "" {
			delete(s.Terrain, k)
		} else {
			s.Terrain[k] = v
		}
	}
	applyMap(s.Objects, p.Objects, &inv.Objects)
	applyMap(s.Walls, p.Walls, &inv.Walls)
	applyMap(s.Tokens, p.Tokens, &inv.Tokens)
	return inv
}

func applyMap[T any](dst, src map[string]*T, inv *map[string]*T) {
	for k, v := range src {
		old, had := dst[k]
		if !had && v == nil {
			delete(src, k)
			continue
		}
		if *inv == nil {
			*inv = map[string]*T{}
		}
		(*inv)[k] = old
		if v == nil {
			delete(dst, k)
		} else {
			dst[k] = v
		}
	}
}

// Clone returns a copy of the scene whose maps can be modified independently.
func (s *Scene) Clone() *Scene {
	c := *s
	c.Terrain = maps.Clone(s.Terrain)
	c.Objects = maps.Clone(s.Objects)
	c.Walls = maps.Clone(s.Walls)
	c.Tokens = maps.Clone(s.Tokens)
	return &c
}
