package game

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charltonadam/tilefall/assets"
	"github.com/charltonadam/tilefall/internal/scene"
	"github.com/charltonadam/tilefall/internal/store"
)

func openGame(t *testing.T, dir string) *Game {
	t.Helper()
	cat, err := assets.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Open(cat, st)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestTemplatesAndTokensAreIndependent(t *testing.T) {
	dir := t.TempDir()
	g := openGame(t, dir)
	if len(g.Library.List()) == 0 {
		t.Fatal("new library has no starter templates")
	}
	id, err := g.Library.Create(NewTemplateParams{Name: "Troll", Size: 2, Color: "#336633", MaxHP: 84})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Apply(&scene.AddToken{TemplateID: id, Cell: scene.Cell{Col: 2, Row: 2}})
	if err != nil {
		t.Fatal(err)
	}
	tokID := res.Result.(scene.IDResult).ID
	token := func() *scene.Token {
		var tok *scene.Token
		g.Table.Read(func(s *scene.Scene) { tok = s.Tokens[tokID] })
		return tok
	}
	if tok := token(); tok.Name != "Troll" || tok.Size != 2 || tok.HP != 84 || tok.MaxHP != 84 {
		t.Fatalf("spawned %+v", tok)
	}

	// editing the template leaves the instance alone...
	name, hp := "Cave troll", 90
	if err := g.Library.Update(TemplateUpdate{ID: id, Name: &name, MaxHP: &hp}); err != nil {
		t.Fatal(err)
	}
	if tok := token(); tok.Name != "Troll" || tok.MaxHP != 84 {
		t.Fatalf("template edit changed the token: %+v", tok)
	}
	// ...and editing the instance leaves the template alone
	dmg := -30
	if _, err := g.Apply(&scene.UpdateToken{ID: tokID, HPDelta: &dmg}); err != nil {
		t.Fatal(err)
	}
	if tpl, _ := g.Library.Get(id); tpl.MaxHP != 90 || tpl.Name != "Cave troll" {
		t.Fatalf("template changed: %+v", tpl)
	}
	if tok := token(); tok.HP != 54 {
		t.Fatalf("hp after 30 damage = %d", tok.HP)
	}

	if _, err := g.Apply(&scene.AddToken{TemplateID: "nope", Cell: scene.Cell{}}); err == nil {
		t.Error("unknown template accepted")
	}

	// the library survives a restart, deletions included
	if err := g.Library.Delete(id); err != nil {
		t.Fatal(err)
	}
	n := len(g.Library.List())
	if _, err := os.Stat(filepath.Join(dir, "library.json")); err != nil {
		t.Fatal(err)
	}
	g2 := openGame(t, dir)
	if got := len(g2.Library.List()); got != n {
		t.Fatalf("reopened library has %d templates, want %d", got, n)
	}
	if _, ok := g2.Library.Get(id); ok {
		t.Error("deleted template came back")
	}
	newID, _ := g2.Library.Create(NewTemplateParams{Name: "Imp"})
	if newID == id {
		t.Error("template id reused after restart")
	}
}
