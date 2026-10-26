// Package assets embeds the built-in asset library: catalog.json (terrain
// types, wall styles and props) and the prop images it references.
package assets

import (
	"embed"

	"github.com/charltonadam/tilefall/internal/scene"
)

//go:embed catalog.json objects
var FS embed.FS

// Catalog parses the embedded catalog.
func Catalog() (*scene.Catalog, error) {
	data, err := FS.ReadFile("catalog.json")
	if err != nil {
		return nil, err
	}
	return scene.ParseCatalog(data)
}
