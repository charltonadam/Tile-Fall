package scene

import (
	"encoding/json"
	"fmt"
)

// Catalog lists the terrain types, placeable assets and wall styles a scene can
// reference by id. Agents pick from it, so names, descriptions and tags matter.
type Catalog struct {
	Terrains   []TerrainType `json:"terrains"`
	Assets     []Asset       `json:"assets"`
	WallStyles []WallStyle   `json:"wallStyles"`

	terrains   map[string]*TerrainType
	assets     map[string]*Asset
	wallStyles map[string]*WallStyle
}

type TerrainType struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Color       string   `json:"color"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type Asset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	File        string   `json:"file"`  // path under /assets/
	Size        float64  `json:"size"`  // default footprint in cells (at scale 1)
	Layer       string   `json:"layer"` // default layer
	Color       string   `json:"color"` // dominant color, used by the server-side preview
	Shape       string   `json:"shape"` // circle or rect, used by the server-side preview
}

type WallStyle struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Color string  `json:"color"`
	Width float64 `json:"width"` // grid units
}

func ParseCatalog(data []byte) (*Catalog, error) {
	c := &Catalog{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}
	c.terrains = map[string]*TerrainType{}
	for i := range c.Terrains {
		t := &c.Terrains[i]
		if _, dup := c.terrains[t.ID]; dup {
			return nil, fmt.Errorf("duplicate terrain %q", t.ID)
		}
		c.terrains[t.ID] = t
	}
	c.assets = map[string]*Asset{}
	for i := range c.Assets {
		a := &c.Assets[i]
		if _, dup := c.assets[a.ID]; dup {
			return nil, fmt.Errorf("duplicate asset %q", a.ID)
		}
		if a.Layer != LayerFloor && a.Layer != LayerOverhead {
			return nil, fmt.Errorf("asset %q: bad layer %q", a.ID, a.Layer)
		}
		c.assets[a.ID] = a
	}
	c.wallStyles = map[string]*WallStyle{}
	for i := range c.WallStyles {
		w := &c.WallStyles[i]
		c.wallStyles[w.ID] = w
	}
	return c, nil
}

func (c *Catalog) Terrain(id string) *TerrainType { return c.terrains[id] }
func (c *Catalog) Asset(id string) *Asset         { return c.assets[id] }
func (c *Catalog) WallStyle(id string) *WallStyle { return c.wallStyles[id] }
