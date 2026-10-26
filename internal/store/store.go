// Package store keeps scenes and uploads on disk as plain files:
//
//	<dir>/scenes/<id>.json   one file per scene
//	<dir>/active             id of the scene to open on startup
//	<dir>/library.json       the DM's token library, shared by all scenes
//	<dir>/uploads/           user-uploaded images, served at /uploads/
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charltonadam/tilefall/internal/scene"
)

type Store struct {
	dir string
}

func Open(dir string) (*Store, error) {
	for _, sub := range []string{"scenes", "uploads"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

func (s *Store) UploadsDir() string { return filepath.Join(s.dir, "uploads") }

var idRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func NewID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "s-" + hex.EncodeToString(b)
}

func (s *Store) scenePath(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid scene id %q", id)
	}
	return filepath.Join(s.dir, "scenes", id+".json"), nil
}

// writeFile writes atomically so a crash never leaves a half-written scene.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) Save(id string, data []byte) error {
	p, err := s.scenePath(id)
	if err != nil {
		return err
	}
	return writeFile(p, data)
}

func (s *Store) Load(id string) (*scene.Scene, error) {
	p, err := s.scenePath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no scene %q", id)
	} else if err != nil {
		return nil, err
	}
	return scene.Decode(data)
}

func (s *Store) Delete(id string) error {
	p, err := s.scenePath(id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

type SceneInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Grid     string    `json:"grid"`
	Modified time.Time `json:"modified"`
}

func (s *Store) List() ([]SceneInfo, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "scenes"))
	if err != nil {
		return nil, err
	}
	out := []SceneInfo{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		sc, err := s.Load(id)
		if err != nil {
			continue
		}
		info, _ := e.Info()
		out = append(out, SceneInfo{ID: id, Name: sc.Name, Width: sc.Width, Height: sc.Height, Grid: string(sc.Grid.Type), Modified: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// LoadLibrary returns the saved token library, or nil if there is none yet.
func (s *Store) LoadLibrary() ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "library.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func (s *Store) SaveLibrary(data []byte) error {
	return writeFile(filepath.Join(s.dir, "library.json"), data)
}

func (s *Store) ActiveID() string {
	b, _ := os.ReadFile(filepath.Join(s.dir, "active"))
	return strings.TrimSpace(string(b))
}

func (s *Store) SetActive(id string) error {
	return writeFile(filepath.Join(s.dir, "active"), []byte(id))
}

// Autosave writes the table's scene whenever it has changed, until ctx is
// done, then saves one last time.
func (s *Store) Autosave(ctx context.Context, t *scene.Table) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Flush(t)
			return
		case <-tick.C:
			s.Flush(t)
		}
	}
}

func (s *Store) Flush(t *scene.Table) {
	if id, data, ok := t.TakeDirty(); ok {
		if err := s.Save(id, data); err != nil {
			log.Printf("autosave %s: %v", id, err)
		}
	}
}

// Switch saves the current scene and makes sc the active one.
func (s *Store) Switch(t *scene.Table, sc *scene.Scene) error {
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	if err := s.Save(sc.ID, data); err != nil {
		return err
	}
	prevID, prev := t.Replace(sc)
	if err := s.Save(prevID, prev); err != nil {
		log.Printf("saving previous scene %s: %v", prevID, err)
	}
	return s.SetActive(sc.ID)
}
