# Tile-Fall

A lightweight digital tabletop for games like D&D. One host computer runs the server; the DM and
players connect from browsers on the local network and all see the same shared battlemap.
Running Claude Code (or any MCP client) can drive the table during play.

## Keeping this file current (read first)

This file is the handoff between sessions. Treat updating it as part of finishing a task, not as
optional cleanup.

- **When to update:** in the same change that adds a package, endpoint, MCP tool, message type,
  data-model field, build/run command, or convention, or that reverses a decision recorded here.
- **What belongs here:** things a future agent can't quickly rediscover from the code: decisions
  and the reasons for them, invariants, cross-cutting conventions, gotchas, current status and next
  steps.
- **What doesn't belong here:** restating code, long API listings (link to the source file
  instead), changelogs (git has those), or session-specific notes.
- **Status section:** move items between Done / Next as work lands. Keep it short and accurate. A
  stale status is worse than none.
- **Decisions:** if you change a decision below, edit it in place and add a one-line "why" so the
  next session doesn't revert it.
- Keep the whole file under roughly 300 lines. Prune before you append.

## Status

**Phase 1 core is working end to end** (verified in a browser with DM + player tabs, and over MCP).

- Done: scene model + command layer + undo/batch (`internal/scene`), JSON persistence with
  autosave, WebSocket sync with DM/player roles, REST API, MCP server (28 tools), server-side PNG
  preview for agents, built-in CC0 asset catalog (20 terrains, 84 props, 5 wall styles), Pixi
  frontend with square/hex grids, pan/zoom/pinch, token drag, terrain brush, object
  place/drag-drop/rotate/scale, wall drawing, inspector, scene settings, scene list/new/load,
  background upload. Movement measuring: token drags show the path and total distance, and there
  is a `measure` MCP tool. Token library: a tray of templates to drag onto the map, token HP
  (map bar for the DM, damage/heal in the inspector, `hpDelta`), and an edit dialog opened by
  double-clicking a token on the map or in the tray.
- Next (rough priority):
  0. Token images: upload from the edit dialog (reuse `/api/uploads`), shown in tray, map and
     preview. `imageUrl` already exists on tokens and templates; the dialog has a URL field only.
  1. Show drag paths to other clients too (an ephemeral WS message, not a command); a ruler
     tool for measuring without moving.
  2. Asset library polish: real textures for terrain instead of flat jittered colors, soft
     blending between terrain types.
  3. Editing gaps: update_wall command (style/points), door gaps in walls, multi-select and box
     select, copy/paste, redo, flood-fill bucket tool.
  4. Play features: initiative tracker, dice roller, token conditions; expose each as MCP
     tools too.
  5. `render_preview` via a connected browser (real artwork) with the server renderer as fallback.
  6. Optional DM password; per-token ownership for players.
  7. Tests for `internal/server` (WS protocol, permissions) and `internal/store`.

## Phase 1 scope

- Shared interactive battlemap; everyone sees the same map; players can move tokens.
- Grid overlay supporting **square** and **hex** (pointy or flat); tokens snap to cells.
- Built-in map creator for the DM that is **fully agent-operable** via MCP.
- **Out of scope:** vision, fog of war, dynamic lighting.

## Commands

The `Makefile` wraps everything (needs GNU make; on Windows `choco install make` or
`winget install ezwinports.make`):

- `make` (= `make start`) installs npm deps if `package.json` changed, rebuilds `web/dist` if any
  frontend source changed, then runs the server. Server flags go in `ARGS`:
  `make ARGS="-port 9000 -mcp-lan"`.
- `make dev` runs the Go server and the Vite dev server together (`make -j2`). Open :5173.
- `make build` (binary in `bin/`), `make test`, `make check`.
- Makefile recipes must run under both cmd.exe and sh (GNU make on Windows only uses sh if one is
  on PATH), so use plain commands, `npm --prefix web ...` rather than `cd`, and no `&&`/`rm -rf`.

Underlying commands:

- `go test ./...` runs the Go tests (scene, grid vectors, MCP end-to-end flow).
- `cd web && npm install` once, then:
  - `npm run build` builds the frontend into `web/dist` (embedded into the Go binary). **Rebuild the
    Go binary after this**, because `go:embed` captures files at compile time.
  - `npm run dev` runs the Vite dev server on :5173 with HMR, proxying `/ws`, `/api`, `/assets`,
    `/uploads` to the Go server on :8080. Run `go run ./cmd/tilefall` alongside it.
  - `npm run check` runs svelte-check (types). `npm test` runs vitest (grid vectors).
- `go run ./cmd/tilefall [-port 8080] [-data data] [-mcp-lan]` prints the DM URL
  (`/?role=dm`), the player LAN URLs, and the `claude mcp add` line.
- Grid math changed? `go test ./internal/scene -run Vectors -update`, then update
  `web/src/lib/grid.ts` until `npm test` passes.
- Preview what an agent sees: `GET /api/preview.png?px=32`.

## Architecture

```
 Browsers ──WebSocket /ws──┐
 REST /api/* ──────────────┼──> game.Game ──> scene.Table (authoritative state, undo, subscribers)
 MCP /mcp (Claude Code) ───┘         │                 │
                                     └── store (data/scenes/*.json, autosave 1s)
```

### Core rule: one command layer

Every in-scene mutation is a `scene.Command` (`internal/scene/commands.go`) applied through
`Table.Apply` / `Table.ApplyBatch`. A command's `plan` validates and returns a **Patch**; the table
applies it, records the inverse patch for undo, bumps `version`, and broadcasts the patch. WS, REST
and MCP are thin adapters and call `game.Apply` / `game.ApplyBatch`, which resolve `add_token`
`templateId` from the token library and then call the Table. **Never mutate scene state from a
transport handler.** Whole-scene operations (new, load, list, delete) live in `internal/game`
because they also touch the store.

- **Adding a command:** add the struct plus `Type()` and `plan()` in `commands.go`, `register` it in
  `init()`, add an MCP tool with `addCommand[...]` in `internal/mcpserver`, and (if UI-facing) send
  it from the frontend. JSON field tags are the wire format for WS, REST and MCP alike, and the
  `jsonschema` tags become MCP tool argument descriptions. Fields without `omitempty` are
  **required** in the MCP schema.
- **Patches** are generic (`meta` replace; `terrain`/`objects`/`walls`/`tokens` maps where
  null/"" deletes). The client only implements patch application (`web/src/lib/patch.ts`), so
  command logic exists only in Go. Don't add client-side copies of command logic.
- **Invariant:** values in scene maps are never mutated in place. Commands copy, modify, and put
  the copy in the patch, because undo patches share pointers.
- Batches are atomic (rolled back on error) and count as one undo step. Undo history is in memory
  only (200 steps) and is cleared on scene switch.
- IDs are short and per-scene (`o12`, `w3`, `t5`) from `Scene.NextID`, so they are easy for agents
  to reference.

### Token library (decided)

- **Templates and tokens are independent.** A template (`scene.TokenTemplate`, kept in
  `game.Library`) holds the defaults; `add_token` with `templateId` copies them, and the token then
  has its own values. Editing a template never changes tokens already on the map, and editing a
  token never changes its template. `Token.templateId` is provenance only. Don't add "sync to
  template" behavior without the user asking for it.
- Spawns always start at full HP (templates have `maxHp` only). `maxHp` 0 means HP isn't tracked.
  `update_token` clamps `hp` to 0..maxHp; `hpDelta` is for damage and healing. Repeated template
  spawns are numbered (Goblin, Goblin 2) in `AddToken.plan`.
- The library is global (shared by all scenes) in `data/library.json`, seeded with a few SRD
  creatures on first run. Library edits are not scene commands: there's no undo, and they're saved
  on every change. The browser edits it over REST (`/api/library`), and DM WebSockets get the
  whole list pushed as `{"t":"library"}`.
- HP is drawn on the map for the DM only. Players still receive it in patches, so there's no
  secrecy until roles/ownership exist.

### Coordinates (decided; keep consistent everywhere)

- **World units are grid units:** 1 unit = distance between adjacent cell centers. Pixels only
  exist at render time (`units * grid.cellSize`), so changing cell size never moves anything.
- **Cells are always `{col,row}`**, including on hex grids, which use offset coordinates: odd-r
  for pointy-top, odd-q for flat-top. Axial/cube coordinates are internal only (rounding). Why:
  rectangles and width×height maps stay intuitive for agents. (This changed the earlier plan of
  exposing axial coordinates.)
- Square grid: the center of cell (c,r) is (c+0.5, r+0.5) and integer coordinates are corners
  (walls go there). Hex size: flat-to-flat = 1 unit.
- **Distance (D&D 5e):** one cell is `grid.distance` `grid.units`, which defaults to 5 ft when
  zero or empty (`Grid.Scale()` / `gridScale()`), so old scenes need no migration. Movement cost
  is `Grid.Steps` / `steps()`: Chebyshev on square grids (diagonals cost one cell, no 5/10
  alternation) and hex distance on hex grids. Drag paths (`web/src/lib/movepath.ts`) sum steps
  between the visited cells, and dragging back over the path truncates it. Nobody drags exactly
  through a corner, so a cell that was only clipped on a diagonal (A→B→C with A and C adjacent)
  is dropped when two of three signals agree: it was left quickly, the samples stayed near the
  shared corner, or the heading in and out pointed A→C. Thresholds are in `CORNER_CUT`, and
  `movepath.test.ts` pins the behavior; retune there, not in MapView.
- Multi-cell tokens anchor at their top-left cell; center = anchor center + (size-1)/2 on each
  axis (in `render.go` and `grid.ts#tokenCenter`).
- Grid math lives in `internal/scene/grid.go`, mirrored in `web/src/lib/grid.ts`, and is pinned by
  `internal/scene/testdata/grid_vectors.json`, which both test suites check. Note that Go
  `math.Round` rounds half away from zero, so the TS mirror uses `roundHalfAway`.

### Backend layout

- `cmd/tilefall`: flags, wiring, graceful shutdown (final save).
- `internal/scene`: model, grid math, regions, commands, Table, catalog types.
- `internal/game`: facade over Table + store (new/load/list/delete scenes, command entry point,
  token library in `library.go`).
- `internal/store`: atomic JSON files under the data dir (`scenes/<id>.json`, `active`,
  `uploads/`).
- `internal/server`: routes (`server.go`) and the WS protocol (`ws.go`, documented at the top of
  the file).
- `internal/mcpserver`: MCP tools (read-only ones like `measure` read the Table directly), plus server `instructions` that teach agents the workflow.
- `internal/render`: simplified PNG renderer (terrain colors, asset color/shape, walls, tokens,
  edge coordinate labels) using `x/image/vector`. The rasterizer doesn't clip, so everything goes
  through `clipPoly`.
- `assets/`: `catalog.json` plus SVG props, embedded. Asset `color`/`shape` exist for the preview
  renderer. All built-in art is original and CC0; keep the library redistributable.
  Prop SVGs use a square `0 0 100 100` viewBox (objects are square sprites; `size` is the side in
  cells), so long props like tables draw a centered band and say "2 cells long" in the
  description. Match the house style: a +4/+4 black shadow at .25 opacity, dark outlines.
  The Objects panel filters by search text and tag, so give new props useful tags.
- `web/embed.go`: embeds `web/dist`. `.gitkeep` must exist there (the build script recreates it)
  or Go won't compile.

### Security / roles

- Roles come from the URL (`?role=dm`); there is no auth in Phase 1. The server enforces that
  players may only send `move_token` (`playerCommands` in `ws.go`).
- `/mcp` only answers loopback clients unless `-mcp-lan` is set (MCP access = DM power).
- The whole mux is wrapped in `http.CrossOriginProtection`, so other websites can't drive the
  table from the DM's browser. The WS accepts the Vite dev origin `localhost:5173`.

### Frontend

- TypeScript + Vite + **Svelte 5 (runes)** for UI, **PixiJS v8** for the map. TS is pinned to 5.x
  because svelte-check doesn't support TS 7 yet.
- `src/lib/state.svelte.ts`: UI state. The scene is `$state.raw` and mutated in place by patches;
  components depend on `app.rev` to re-read it.
- `src/lib/map/MapView.ts`: imperative renderer plus input. Terrain is drawn in 16×16-cell chunk
  Graphics and only dirty chunks are redrawn. Hit testing is done manually, not via Pixi events.
  Gestures cover pan, token drag (snaps), object drag (Shift snaps), paint (one `fill_terrain`
  per stroke, so one undo step), walls (corner snap, Shift for free placement), and pinch.
- Sidebar shows either the current tool's panel + Inspector, or (⚙ at the bottom of the toolbar,
  `app.scenePanel`) scene settings, scene list and agent access. It's a panel toggle, not a map
  tool: the active tool keeps working. Picking a tool or selecting something closes it.
- Optimistic updates (drag, paint) mutate the local scene. If the server rejects, `App.svelte`
  calls `conn.sync()` to fetch a fresh snapshot.
- Vite `build.assetsDir` is `static` because `/assets/` belongs to the Go asset library.

## Conventions

- Development happens on Windows (PowerShell + Git Bash). Keep scripts cross-platform: Go or npm
  scripts, never shell-specific ones.
- Keep commands small, explicit and JSON-serializable. Return created IDs. Errors should be
  readable by an agent (MCP returns them as tool errors, not protocol errors).
- Test the command layer and grid math. `internal/mcpserver/mcpserver_test.go` is the end-to-end
  check that tools build a map, and setting `TILEFALL_PREVIEW_OUT=path.png` saves the preview it
  renders.
