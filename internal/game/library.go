package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/charltonadam/tilefall/internal/scene"
)

// Library is the DM's token library: templates that tokens are spawned from.
// It is shared by every scene and saved to its own file on each change. Edits
// are not scene commands, so they are not undoable and never touch tokens
// already on a map.
type Library struct {
	mu        sync.Mutex
	save      func([]byte) error
	nextID    int
	templates map[string]*scene.TokenTemplate
	subs      map[chan struct{}]struct{}
}

type libraryFile struct {
	NextID    int                    `json:"nextId"`
	Templates []*scene.TokenTemplate `json:"templates"`
}

// starterTemplates seed a new library so the tray is useful immediately
// (hit points from the 5e SRD).
var starterTemplates = []scene.TokenTemplate{
	{Name: "Adventurer", Size: 1, Color: "#2e86de", MaxHP: 10},
	{Name: "Goblin", Size: 1, Color: "#4f8a3a", MaxHP: 7},
	{Name: "Bandit", Size: 1, Color: "#8e5a2b", MaxHP: 11},
	{Name: "Wolf", Size: 1, Color: "#7f8c8d", MaxHP: 11},
	{Name: "Skeleton", Size: 1, Color: "#d5cfc0", MaxHP: 13},
	{Name: "Orc", Size: 1, Color: "#5d6d3a", MaxHP: 15},
	{Name: "Ogre", Size: 2, Color: "#a0522d", MaxHP: 59},
}

// openLibrary decodes a saved library; data nil means start from the
// starter templates.
func openLibrary(data []byte, save func([]byte) error) (*Library, error) {
	l := &Library{save: save, templates: map[string]*scene.TokenTemplate{}, subs: map[chan struct{}]struct{}{}}
	if data == nil {
		for _, t := range starterTemplates {
			t.ID = l.newID()
			l.templates[t.ID] = &t
		}
		return l, l.persist()
	}
	var f libraryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("library.json: %w", err)
	}
	l.nextID = f.NextID
	for _, t := range f.Templates {
		l.templates[t.ID] = t
	}
	return l, nil
}

func (l *Library) newID() string {
	l.nextID++
	return "tpl" + strconv.Itoa(l.nextID)
}

// List returns the templates sorted by name. The values must not be modified.
func (l *Library) List() []*scene.TokenTemplate {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.listLocked()
}

func (l *Library) listLocked() []*scene.TokenTemplate {
	out := make([]*scene.TokenTemplate, 0, len(l.templates))
	for _, t := range l.templates {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (l *Library) Get(id string) (*scene.TokenTemplate, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.templates[id]
	return t, ok
}

type NewTemplateParams struct {
	Name     string `json:"name"`
	Size     int    `json:"size,omitempty" jsonschema:"footprint in cells: 1 medium (default), 2 large, 3 huge, 4 gargantuan"`
	Color    string `json:"color,omitempty" jsonschema:"#rrggbb (default #c0392b)"`
	ImageURL string `json:"imageUrl,omitempty"`
	MaxHP    int    `json:"maxHp,omitempty" jsonschema:"maximum hit points tokens spawn with (0 = not tracked)"`
}

// Create adds a template and returns its id.
func (l *Library) Create(p NewTemplateParams) (string, error) {
	t := &scene.TokenTemplate{Name: p.Name, Size: p.Size, Color: p.Color, ImageURL: p.ImageURL, MaxHP: p.MaxHP}
	if t.Size == 0 {
		t.Size = 1
	}
	if t.Color == "" {
		t.Color = "#c0392b"
	}
	if err := t.Validate(); err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t.ID = l.newID()
	l.templates[t.ID] = t
	return t.ID, l.changedLocked()
}

type TemplateUpdate struct {
	ID       string  `json:"id"`
	Name     *string `json:"name,omitempty"`
	Size     *int    `json:"size,omitempty"`
	Color    *string `json:"color,omitempty"`
	ImageURL *string `json:"imageUrl,omitempty"`
	MaxHP    *int    `json:"maxHp,omitempty"`
}

// Update changes a template's defaults. Tokens already spawned from it keep
// their own values.
func (l *Library) Update(u TemplateUpdate) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.templates[u.ID]
	if old == nil {
		return fmt.Errorf("no token template %q", u.ID)
	}
	t := *old
	if u.Name != nil {
		t.Name = *u.Name
	}
	if u.Size != nil {
		t.Size = *u.Size
	}
	if u.Color != nil {
		t.Color = *u.Color
	}
	if u.ImageURL != nil {
		t.ImageURL = *u.ImageURL
	}
	if u.MaxHP != nil {
		t.MaxHP = *u.MaxHP
	}
	if err := t.Validate(); err != nil {
		return err
	}
	l.templates[t.ID] = &t
	return l.changedLocked()
}

func (l *Library) Delete(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.templates[id] == nil {
		return fmt.Errorf("no token template %q", id)
	}
	delete(l.templates, id)
	return l.changedLocked()
}

// Subscribe returns a channel that receives a value whenever the library
// changes (signals coalesce; read List for the new contents).
func (l *Library) Subscribe() (<-chan struct{}, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := make(chan struct{}, 1)
	l.subs[ch] = struct{}{}
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.subs, ch)
	}
}

func (l *Library) changedLocked() error {
	for ch := range l.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return l.persist()
}

func (l *Library) persist() error {
	data, err := json.MarshalIndent(libraryFile{NextID: l.nextID, Templates: l.listLocked()}, "", " ")
	if err != nil {
		return err
	}
	if err := l.save(data); err != nil {
		return errors.New("saving token library: " + err.Error())
	}
	return nil
}
