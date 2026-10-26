package scene

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Event is sent to subscribers. Kind is "snapshot" (Data is a full Scene) or
// "patch" (Data is a Patch). Data is pre-encoded JSON.
type Event struct {
	Kind string
	Data json.RawMessage
}

// Subscriber receives events. If it falls too far behind its channel is
// closed; the consumer should reconnect and will get a fresh snapshot.
type Subscriber struct {
	C chan Event
}

const maxUndo = 200

// Table holds the active scene and is the only way to change it. It is safe
// for concurrent use.
type Table struct {
	mu    sync.Mutex
	cat   *Catalog
	scene *Scene
	undo  []*Patch
	subs  map[*Subscriber]struct{}
	dirty bool
}

func NewTable(cat *Catalog, s *Scene) *Table {
	return &Table{cat: cat, scene: s, subs: map[*Subscriber]struct{}{}}
}

func (t *Table) Catalog() *Catalog { return t.cat }

// ApplyResult is returned to the caller of Apply.
type ApplyResult struct {
	Version uint64 `json:"version"`
	Result  any    `json:"result,omitempty"`
}

// Apply validates and applies one command, broadcasting the resulting patch.
func (t *Table) Apply(cmd Command) (ApplyResult, error) {
	return t.ApplyBatch([]Command{cmd})
}

// ApplyBatch applies commands in order as one atomic change: if any command
// fails, none of them take effect. It is broadcast and undone as one patch.
// The result is the single command's result, or a slice of results.
func (t *Table) ApplyBatch(cmds []Command) (ApplyResult, error) {
	if len(cmds) == 0 {
		return ApplyResult{}, errors.New("no commands")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	var applied, inverses []*Patch
	results := make([]any, 0, len(cmds))
	for i, cmd := range cmds {
		p, res, err := cmd.plan(t.scene, t.cat)
		if err == nil && p == nil {
			err = errors.New("command produced no patch")
		}
		if err != nil {
			for j := len(inverses) - 1; j >= 0; j-- {
				t.scene.apply(inverses[j])
			}
			if len(cmds) > 1 {
				err = fmt.Errorf("command %d (%s): %w", i, cmd.Type(), err)
			}
			return ApplyResult{}, err
		}
		inverses = append(inverses, t.scene.apply(p))
		applied = append(applied, p)
		results = append(results, res)
	}
	combined := mergePatches(applied, false)
	inverse := mergePatches(inverses, true)

	var out ApplyResult
	if len(results) == 1 {
		out.Result = results[0]
	} else {
		out.Result = results
	}
	if combined.Empty() {
		out.Version = t.scene.Version
		return out, nil
	}
	t.pushUndo(inverse)
	out.Version = t.commit(combined)
	return out, nil
}

// Undo reverts the most recent change.
func (t *Table) Undo() (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.undo) == 0 {
		return t.scene.Version, errors.New("nothing to undo")
	}
	p := t.undo[len(t.undo)-1]
	t.undo = t.undo[:len(t.undo)-1]
	t.scene.apply(p)
	return t.commit(p), nil
}

func (t *Table) pushUndo(p *Patch) {
	t.undo = append(t.undo, p)
	if len(t.undo) > maxUndo {
		t.undo = t.undo[len(t.undo)-maxUndo:]
	}
}

// commit bumps the version and broadcasts p. Caller holds t.mu.
func (t *Table) commit(p *Patch) uint64 {
	t.scene.Version++
	p.Version = t.scene.Version
	t.dirty = true
	data, err := json.Marshal(p)
	if err != nil {
		panic(err) // all patch fields are plain data
	}
	t.broadcast(Event{Kind: "patch", Data: data})
	return t.scene.Version
}

func (t *Table) broadcast(ev Event) {
	for s := range t.subs {
		select {
		case s.C <- ev:
		default:
			close(s.C)
			delete(t.subs, s)
		}
	}
}

// mergePatches combines sequential patches. For forward patches later values
// win; for inverse patches (keepFirst) the earliest value wins, since it is the
// state before the whole sequence.
func mergePatches(ps []*Patch, keepFirst bool) *Patch {
	if len(ps) == 1 {
		return ps[0]
	}
	out := &Patch{}
	if keepFirst {
		// apply inverses in reverse order: the first patch's inverse is applied last
		for i, j := 0, len(ps)-1; i < j; i, j = i+1, j-1 {
			ps[i], ps[j] = ps[j], ps[i]
		}
	}
	for _, p := range ps {
		if p.Meta != nil {
			out.Meta = p.Meta
		}
		for k, v := range p.Terrain {
			out.setTerrain(k, v)
		}
		for k, v := range p.Objects {
			out.setObject(k, v)
		}
		for k, v := range p.Walls {
			out.setWall(k, v)
		}
		for k, v := range p.Tokens {
			out.setToken(k, v)
		}
	}
	return out
}

// Subscribe registers for events and returns the current snapshot, atomically.
func (t *Table) Subscribe() (*Subscriber, json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := &Subscriber{C: make(chan Event, 256)}
	t.subs[s] = struct{}{}
	return s, t.snapshotLocked()
}

func (t *Table) Unsubscribe(s *Subscriber) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.subs[s]; ok {
		delete(t.subs, s)
		close(s.C)
	}
}

func (t *Table) snapshotLocked() json.RawMessage {
	data, err := json.Marshal(t.scene)
	if err != nil {
		panic(err)
	}
	return data
}

// Snapshot returns the current scene as JSON.
func (t *Table) Snapshot() json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

// Read calls f with a read-only view of the scene. f must not retain it.
func (t *Table) Read(f func(s *Scene)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f(t.scene)
}

// SceneID returns the active scene's id.
func (t *Table) SceneID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scene.ID
}

// Replace swaps in a different scene (new or loaded), clears undo history and
// sends every subscriber a snapshot. It returns the previous scene's JSON so the
// caller can save it.
func (t *Table) Replace(s *Scene) (prevID string, prev json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prevID, prev = t.scene.ID, t.snapshotLocked()
	t.scene = s
	t.undo = nil
	t.dirty = true
	t.broadcast(Event{Kind: "snapshot", Data: t.snapshotLocked()})
	return prevID, prev
}

// TakeDirty returns the scene JSON if it changed since the last call.
func (t *Table) TakeDirty() (id string, data json.RawMessage, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.dirty {
		return "", nil, false
	}
	t.dirty = false
	return t.scene.ID, t.snapshotLocked(), true
}
