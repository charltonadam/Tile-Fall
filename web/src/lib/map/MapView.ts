// Pixi renderer and pointer interaction for the battlemap. Rendering is a
// function of the scene: MapView never owns game state, it only draws the
// scene it is given and turns gestures into commands (via MapHost).

import { Application, Assets, Container, Graphics, Sprite, Text, Texture } from "pixi.js";
import {
  cellCenter,
  cellCorners,
  cellKey,
  cellsInRadius,
  gridScale,
  inBounds,
  mapExtent,
  nearestCorner,
  parseCellKey,
  pointToCell,
  tokenCenter,
} from "../grid";
import { hpColor } from "../hp";
import { MovePath } from "../movepath";
import type { Changes } from "../patch";
import type { Selection, Tool } from "../state.svelte";
import type { Asset, Catalog, Cell, Command, Point, Scene, SceneObject, TokenTemplate } from "../types";

/** What the map needs from the rest of the app. */
export interface MapHost {
  readonly isDM: boolean;
  readonly tool: Tool;
  readonly terrain: string;
  readonly brush: number;
  readonly assetId: string;
  readonly wallStyle: string;
  /** library template the token tool places ("" = a blank token) */
  readonly templateId: string;
  template(id: string): TokenTemplate | undefined;
  selection: Selection;
  /** Open the edit dialog for a token on the map (DM double-click). */
  editToken(id: string): void;
  /** Send a command; on failure the host resyncs the scene from the server. */
  send(cmd: Command): Promise<{ ok: boolean; result?: unknown }>;
  /** Tell the host the scene was changed locally (optimistic update). */
  touched(): void;
}

const CHUNK = 16;
const TEMPLATE_MIME = "application/x-tilefall-template";
const NEUTRAL = 0x2e2e34;
const SELECT_COLOR = 0xffd34d;

type Gesture =
  | { kind: "pan"; sx: number; sy: number; wx: number; wy: number; moved: boolean; clickClears: boolean }
  | { kind: "token"; id: string; cell: Cell; grab: Point; moved: boolean; path: MovePath }
  | { kind: "object"; id: string; grab: Point; pos: Point; moved: boolean }
  | { kind: "paint"; terrain: string; cells: Map<string, Cell> }
  | { kind: "pinch"; d0: number; z0: number; mid: Point };

const hexColor = (s: string) => parseInt(s.slice(1), 16) || 0;

function vary(color: number, col: number, row: number) {
  // deterministic per-cell brightness jitter so flat terrain reads as texture
  let h = (col * 374761393 + row * 668265263) | 0;
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  const f = 0.95 + (((h >>> 0) % 1000) / 1000) * 0.1;
  const r = Math.min(255, ((color >> 16) & 255) * f);
  const g = Math.min(255, ((color >> 8) & 255) * f);
  const b = Math.min(255, (color & 255) * f);
  return (r << 16) | (g << 8) | b;
}

export class MapView {
  private app = new Application();
  private world = new Container();
  private base = new Graphics();
  private bg = new Container();
  private terrainLayer = new Container();
  private floor = new Container();
  private walls = new Graphics();
  private gridG = new Graphics();
  private overhead = new Container();
  private tokens = new Container();
  private overlay = new Graphics();
  // distance readout while dragging a token; drawn at a fixed screen size
  private measure = new Container();
  private measureBg = new Graphics();
  private measureText = new Text({ text: "", style: { fontFamily: "system-ui, sans-serif", fontSize: 15, fontWeight: "bold", fill: 0xffffff } });

  private scene: Scene | null = null;
  private sceneId = "";
  private catalog: Catalog | null = null;
  private assets = new Map<string, Asset>();
  private textures = new Map<string, Texture>();
  private chunks = new Map<string, Graphics>();
  private sprites = new Map<string, Sprite>();
  private tokenViews = new Map<string, { root: Container; sig: string }>();

  private zoom = 1;
  private gesture: Gesture | null = null;
  private pointers = new Map<number, Point>();
  private hover: Point | null = null;
  // where a library template being dragged from the tray would land
  private dropHover: Point | null = null;
  private shift = false;
  private wallDraft: Point[] = [];
  private ready = false;
  private cleanup: (() => void)[] = [];

  constructor(private host: MapHost) {}

  async init(el: HTMLElement) {
    await this.app.init({ resizeTo: el, background: 0x1b1b1f, antialias: true, resolution: window.devicePixelRatio, autoDensity: true });
    el.appendChild(this.app.canvas);
    this.app.canvas.style.touchAction = "none";
    this.floor.sortableChildren = true;
    this.overhead.sortableChildren = true;
    this.world.addChild(this.base, this.bg, this.terrainLayer, this.floor, this.walls, this.gridG, this.overhead, this.tokens, this.overlay, this.measure);
    this.measure.addChild(this.measureBg, this.measureText);
    this.measure.visible = false;
    this.app.stage.addChild(this.world);
    this.bindEvents();
    this.ready = true;
    if (this.scene) this.update(this.scene, null);
  }

  destroy() {
    this.cleanup.forEach((f) => f());
    if (this.ready) this.app.destroy(true, { children: true });
  }

  private get S() {
    return this.scene?.grid.cellSize ?? 70;
  }

  // ---------- data in ----------

  async setCatalog(cat: Catalog) {
    this.catalog = cat;
    this.assets = new Map(cat.assets.map((a) => [a.id, a]));
    this.terrainColor.clear();
    if (this.scene && this.ready) this.update(this.scene, null);
    await Promise.all(
      cat.assets.map(async (a) => {
        try {
          const tex = await Assets.load<Texture>({ src: `/assets/${a.file}`, data: { resolution: 4 } });
          this.textures.set(a.id, tex);
        } catch (e) {
          console.warn("asset failed to load", a.id, e);
        }
      }),
    );
    if (this.scene && this.ready) this.syncObjects();
  }

  /** Draw scene. `changes` null means everything changed (snapshot). */
  update(scene: Scene, changes: Changes | null) {
    this.scene = scene;
    if (!this.ready) return;
    const all = !changes || changes.all;
    if (scene.id !== this.sceneId) {
      this.sceneId = scene.id;
      this.wallDraft = [];
      this.fit();
    }
    if (all) {
      this.drawBase();
      this.drawAllTerrain();
      this.drawGrid();
    } else if (changes.terrainKeys.length) {
      this.drawTerrainKeys(changes.terrainKeys);
    }
    if (all || changes.objects) this.syncObjects();
    if (all || changes.walls) this.drawWalls();
    if (all || changes.tokens) this.syncTokens();
    this.drawOverlay();
  }

  /** Redraw the selection/brush overlay (e.g. after the host changes selection or tool). */
  refreshOverlay() {
    if (this.tool !== "wall") this.wallDraft = [];
    if (this.ready) this.drawOverlay();
  }

  private get tool(): Tool {
    return this.host.isDM ? this.host.tool : "select";
  }

  // ---------- view ----------

  fit() {
    if (!this.scene) return;
    const ext = mapExtent(this.scene.grid, this.scene.width, this.scene.height);
    const w = ext.x * this.S, h = ext.y * this.S;
    const sw = this.app.screen.width, sh = this.app.screen.height;
    this.zoom = Math.max(0.05, Math.min(2, Math.min(sw / (w + 80), sh / (h + 80))));
    this.world.scale.set(this.zoom);
    this.world.position.set((sw - w * this.zoom) / 2, (sh - h * this.zoom) / 2);
  }

  private zoomAt(sx: number, sy: number, factor: number) {
    const z = Math.max(0.05, Math.min(6, this.zoom * factor));
    const wx = (sx - this.world.x) / this.zoom, wy = (sy - this.world.y) / this.zoom;
    this.zoom = z;
    this.world.scale.set(z);
    this.world.position.set(sx - wx * z, sy - wy * z);
  }

  /** Screen (canvas CSS px) to grid units. */
  private toUnits(sx: number, sy: number): Point {
    return { x: (sx - this.world.x) / this.zoom / this.S, y: (sy - this.world.y) / this.zoom / this.S };
  }

  private canvasPoint(e: { clientX: number; clientY: number }) {
    const r = this.app.canvas.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }

  // ---------- drawing ----------

  private poly(pts: Point[]) {
    const S = this.S;
    return pts.flatMap((p) => [p.x * S, p.y * S]);
  }

  private drawBase() {
    const s = this.scene!;
    this.base.clear();
    for (let row = 0; row < s.height; row++)
      for (let col = 0; col < s.width; col++) this.base.poly(this.poly(cellCorners(s.grid, { col, row })));
    this.base.fill({ color: NEUTRAL });

    this.bg.removeChildren().forEach((c) => c.destroy());
    const b = s.background;
    if (b) {
      Assets.load<Texture>(b.url)
        .then((tex) => {
          if (this.scene?.background?.url !== b.url) return;
          const sp = new Sprite(tex);
          sp.position.set(b.x * this.S, b.y * this.S);
          sp.width = b.width * this.S;
          sp.height = b.height * this.S;
          this.bg.removeChildren().forEach((c) => c.destroy());
          this.bg.addChild(sp);
        })
        .catch((e) => console.warn("background failed to load", e));
    }
  }

  private terrainColor = new Map<string, number>();

  private colorOf(id: string) {
    let c = this.terrainColor.get(id);
    if (c === undefined) {
      const t = this.catalog?.terrains.find((t) => t.id === id);
      c = t ? hexColor(t.color) : 0xff00ff;
      if (this.catalog) this.terrainColor.set(id, c);
    }
    return c;
  }

  private drawAllTerrain() {
    for (const g of this.chunks.values()) g.destroy();
    this.chunks.clear();
    const s = this.scene!;
    const keys = new Set<string>();
    for (let cy = 0; cy * CHUNK < s.height; cy++) for (let cx = 0; cx * CHUNK < s.width; cx++) keys.add(`${cx},${cy}`);
    for (const k of keys) this.drawChunk(k);
  }

  private drawTerrainKeys(cellKeys: string[]) {
    const chunks = new Set<string>();
    for (const k of cellKeys) {
      const c = parseCellKey(k);
      chunks.add(`${Math.floor(c.col / CHUNK)},${Math.floor(c.row / CHUNK)}`);
    }
    for (const k of chunks) this.drawChunk(k);
  }

  private drawChunk(key: string) {
    const s = this.scene!;
    const [cx, cy] = key.split(",").map(Number);
    let g = this.chunks.get(key);
    if (!g) {
      g = new Graphics();
      this.chunks.set(key, g);
      this.terrainLayer.addChild(g);
    }
    g.clear();
    for (let row = cy * CHUNK; row < Math.min(s.height, (cy + 1) * CHUNK); row++) {
      for (let col = cx * CHUNK; col < Math.min(s.width, (cx + 1) * CHUNK); col++) {
        const t = s.terrain[`${col},${row}`];
        if (!t) continue;
        g.poly(this.poly(cellCorners(s.grid, { col, row }))).fill({ color: vary(this.colorOf(t), col, row) });
      }
    }
  }

  private drawGrid() {
    const s = this.scene!;
    const g = this.gridG;
    g.clear();
    if (!s.grid.visible) return;
    const S = this.S;
    if (s.grid.type === "square") {
      for (let col = 0; col <= s.width; col++) g.moveTo(col * S, 0).lineTo(col * S, s.height * S);
      for (let row = 0; row <= s.height; row++) g.moveTo(0, row * S).lineTo(s.width * S, row * S);
    } else {
      for (let row = 0; row < s.height; row++)
        for (let col = 0; col < s.width; col++) g.poly(this.poly(cellCorners(s.grid, { col, row })), true);
    }
    g.stroke({ width: Math.max(1, S / 50), color: hexColor(s.grid.color), alpha: s.grid.opacity });
  }

  private syncObjects() {
    const s = this.scene!;
    for (const [id, sp] of this.sprites) {
      if (!s.objects[id]) {
        sp.destroy();
        this.sprites.delete(id);
      }
    }
    for (const o of Object.values(s.objects)) {
      let sp = this.sprites.get(o.id);
      const tex = this.textures.get(o.assetId) ?? Texture.WHITE;
      if (!sp) {
        sp = new Sprite(tex);
        sp.anchor.set(0.5);
        this.sprites.set(o.id, sp);
      }
      sp.texture = tex;
      const parent = o.layer === "overhead" ? this.overhead : this.floor;
      if (sp.parent !== parent) parent.addChild(sp);
      this.placeSprite(sp, o, { x: o.x, y: o.y });
    }
  }

  private placeSprite(sp: Sprite, o: SceneObject, pos: Point) {
    const a = this.assets.get(o.assetId);
    const size = (a?.size ?? 1) * o.scale * this.S;
    sp.position.set(pos.x * this.S, pos.y * this.S);
    sp.width = size;
    sp.height = size;
    sp.rotation = (o.rotation * Math.PI) / 180;
    sp.tint = this.textures.has(o.assetId) ? 0xffffff : hexColor(a?.color ?? "#ff00ff");
    sp.zIndex = o.z * 1e6 + (parseInt(o.id.slice(1)) || 0);
  }

  private drawWalls() {
    const s = this.scene!;
    const g = this.walls;
    g.clear();
    for (const w of Object.values(s.walls)) {
      const st = this.catalog?.wallStyles.find((x) => x.id === w.style);
      const pts = this.poly(w.points);
      g.moveTo(pts[0], pts[1]);
      for (let i = 2; i < pts.length; i += 2) g.lineTo(pts[i], pts[i + 1]);
      if (w.closed) g.closePath();
      g.stroke({ width: (st?.width ?? 0.25) * this.S, color: hexColor(st?.color ?? "#222222"), cap: "round", join: "round" });
    }
  }

  private syncTokens() {
    const s = this.scene!;
    const S = this.S;
    for (const [id, v] of this.tokenViews) {
      if (!s.tokens[id]) {
        v.root.destroy({ children: true });
        this.tokenViews.delete(id);
      }
    }
    for (const t of Object.values(s.tokens)) {
      // hp shows on the map for the DM only (players still receive it; there is no secrecy yet)
      const showHp = this.host.isDM && t.maxHp > 0;
      const sig = `${t.name}|${t.size}|${t.color}|${t.imageUrl ?? ""}|${S}|${showHp ? `${t.hp}/${t.maxHp}` : ""}`;
      let v = this.tokenViews.get(t.id);
      if (!v || v.sig !== sig) {
        v?.root.destroy({ children: true });
        const root = new Container();
        const r = 0.42 * t.size * S;
        const disc = new Graphics().circle(0, 0, r).fill({ color: hexColor(t.color) }).stroke({ width: S * 0.06, color: 0xffffff });
        // a downed token (0 hp) is drawn faded
        const face = new Container();
        face.alpha = disc.alpha = showHp && t.hp === 0 ? 0.45 : 1;
        root.addChild(disc, face);
        if (t.imageUrl) {
          Assets.load<Texture>(t.imageUrl)
            .then((tex) => {
              if (root.destroyed) return;
              const sp = new Sprite(tex);
              sp.anchor.set(0.5);
              sp.width = sp.height = 2 * r * 0.94;
              const mask = new Graphics().circle(0, 0, r * 0.94).fill(0xffffff);
              sp.mask = mask;
              face.addChild(mask, sp);
            })
            .catch(() => {});
        } else {
          const initial = new Text({ text: (t.name[0] ?? "?").toUpperCase(), style: { fontFamily: "system-ui, sans-serif", fontSize: r, fill: 0xffffff, fontWeight: "bold" } });
          initial.anchor.set(0.5);
          initial.resolution = 3;
          face.addChild(initial);
        }
        let below = r + S * 0.04;
        if (showHp) {
          const w = Math.min(2 * r, S * 0.9), h = S * 0.09, frac = t.hp / t.maxHp;
          const bar = new Graphics().roundRect(-w / 2, below, w, h, h / 2).fill({ color: 0x111114, alpha: 0.85 });
          if (frac > 0) bar.roundRect(-w / 2, below, w * frac, h, h / 2).fill({ color: hexColor(hpColor(frac)) });
          root.addChild(bar);
          below += h + S * 0.02;
        }
        const label = new Text({ text: t.name, style: { fontFamily: "system-ui, sans-serif", fontSize: S * 0.22, fill: 0xffffff, stroke: { color: 0x000000, width: S * 0.06 } } });
        label.anchor.set(0.5, 0);
        label.position.set(0, below);
        label.resolution = 3;
        root.addChild(label);
        v = { root, sig };
        this.tokenViews.set(t.id, v);
        this.tokens.addChild(root);
      }
      const g = this.gesture;
      const cell = g?.kind === "token" && g.id === t.id ? g.cell : t.cell;
      const c = tokenCenter(s.grid, cell, t.size);
      v.root.position.set(c.x * S, c.y * S);
    }
  }

  private drawOverlay() {
    const s = this.scene;
    const g = this.overlay;
    g.clear();
    if (!s) return;
    const S = this.S;
    const lw = 2 / this.zoom;
    const sel = this.host.selection;
    if (sel?.kind === "token" && s.tokens[sel.id]) {
      const t = s.tokens[sel.id];
      const gs = this.gesture;
      const c = tokenCenter(s.grid, gs?.kind === "token" && gs.id === t.id ? gs.cell : t.cell, t.size);
      g.circle(c.x * S, c.y * S, (0.42 * t.size + 0.08) * S).stroke({ width: lw * 1.5, color: SELECT_COLOR });
    } else if (sel?.kind === "object" && s.objects[sel.id]) {
      const o = s.objects[sel.id];
      const gs = this.gesture;
      const pos = gs?.kind === "object" && gs.id === o.id ? gs.pos : o;
      const half = ((this.assets.get(o.assetId)?.size ?? 1) * o.scale * S) / 2;
      g.rect(pos.x * S - half, pos.y * S - half, half * 2, half * 2).stroke({ width: lw, color: SELECT_COLOR });
    } else if (sel?.kind === "wall" && s.walls[sel.id]) {
      const w = s.walls[sel.id];
      const pts = this.poly(w.points);
      g.moveTo(pts[0], pts[1]);
      for (let i = 2; i < pts.length; i += 2) g.lineTo(pts[i], pts[i + 1]);
      if (w.closed) g.closePath();
      g.stroke({ width: lw * 2, color: SELECT_COLOR });
    }
    this.drawMovePath();

    const tool = this.tool;
    const spawnAt = this.dropHover ?? (tool === "token" ? this.hover : null);
    if (spawnAt) {
      // footprint of the token that would be spawned
      const size = this.templateSize(this.host.templateId);
      const a = this.anchorAt(spawnAt, size);
      for (let dr = 0; dr < size; dr++)
        for (let dc = 0; dc < size; dc++) g.poly(this.poly(cellCorners(s.grid, { col: a.col + dc, row: a.row + dr })));
      g.stroke({ width: lw, color: 0xffffff, alpha: 0.9 });
    } else if (this.hover && (tool === "terrain" || tool === "erase")) {
      for (const c of this.brushCells(pointToCell(s.grid, this.hover))) g.poly(this.poly(cellCorners(s.grid, c)));
      g.stroke({ width: lw, color: tool === "erase" ? 0xff6b6b : 0xffffff, alpha: 0.9 });
    }
    if (tool === "wall") {
      const pts = [...this.wallDraft];
      if (this.hover) pts.push(this.wallSnap(this.hover));
      if (pts.length) {
        const st = this.catalog?.wallStyles.find((x) => x.id === this.host.wallStyle);
        const px = this.poly(pts);
        g.moveTo(px[0], px[1]);
        for (let i = 2; i < px.length; i += 2) g.lineTo(px[i], px[i + 1]);
        g.stroke({ width: (st?.width ?? 0.25) * S, color: hexColor(st?.color ?? "#222222"), alpha: 0.6, cap: "round", join: "round" });
        for (const p of pts) g.circle(p.x * S, p.y * S, 4 / this.zoom).fill({ color: SELECT_COLOR });
      }
    }
  }

  /** While dragging a token: the path it took through cells and the total distance moved. */
  private drawMovePath() {
    const s = this.scene!;
    const gs = this.gesture;
    this.measure.visible = false;
    if (gs?.kind !== "token") return;
    const t = s.tokens[gs.id];
    const cells = gs.path.cells;
    if (!t || cells.length < 2) return;
    const S = this.S, lw = 2 / this.zoom;
    const g = this.overlay;
    const pts = cells.map((c) => tokenCenter(s.grid, c, t.size));
    const line = () => {
      g.moveTo(pts[0].x * S, pts[0].y * S);
      for (const p of pts.slice(1)) g.lineTo(p.x * S, p.y * S);
    };
    line();
    g.stroke({ width: lw * 3, color: 0x000000, alpha: 0.5, cap: "round", join: "round" });
    line();
    g.stroke({ width: lw * 1.5, color: SELECT_COLOR, cap: "round", join: "round" });
    for (const p of pts.slice(0, -1)) g.circle(p.x * S, p.y * S, 3.5 / this.zoom).fill({ color: SELECT_COLOR });

    const { per, units } = gridScale(s.grid);
    this.measureText.text = `${+(gs.path.steps * per).toFixed(1)} ${units}`;
    const pad = 6, w = this.measureText.width + pad * 2, h = this.measureText.height + pad;
    this.measureBg.clear().roundRect(0, 0, w, h, 6).fill({ color: 0x111114, alpha: 0.85 }).stroke({ width: 1, color: SELECT_COLOR });
    this.measureText.position.set(pad, pad / 2);
    // up and to the right of the token, at constant screen size
    const end = pts[pts.length - 1];
    const r = (0.42 * t.size + 0.08) * S;
    this.measure.scale.set(1 / this.zoom);
    this.measure.position.set(end.x * S + r * 0.7, end.y * S - r * 0.7 - h / this.zoom);
    this.measure.visible = true;
  }

  private brushCells(c: Cell): Cell[] {
    const s = this.scene!;
    return cellsInRadius(s.grid, s.width, s.height, c, this.host.brush - 1);
  }

  private wallSnap(p: Point): Point {
    if (this.shift) return p;
    const c = nearestCorner(this.scene!.grid, p);
    return { x: Math.round(c.x * 1000) / 1000, y: Math.round(c.y * 1000) / 1000 };
  }

  // ---------- hit testing ----------

  private hitToken(p: Point) {
    const s = this.scene!;
    let best: string | null = null;
    for (const t of Object.values(s.tokens)) {
      const c = tokenCenter(s.grid, t.cell, t.size);
      if (Math.hypot(c.x - p.x, c.y - p.y) <= 0.45 * t.size) best = t.id;
    }
    return best;
  }

  private hitObject(p: Point) {
    const s = this.scene!;
    const objs = Object.values(s.objects).sort((a, b) => {
      if (a.layer !== b.layer) return a.layer === "overhead" ? -1 : 1;
      return b.z - a.z || (parseInt(b.id.slice(1)) || 0) - (parseInt(a.id.slice(1)) || 0);
    });
    for (const o of objs) {
      const half = ((this.assets.get(o.assetId)?.size ?? 1) * o.scale) / 2;
      if (Math.abs(p.x - o.x) <= half * 0.8 && Math.abs(p.y - o.y) <= half * 0.8) return o.id;
    }
    return null;
  }

  private hitWall(p: Point) {
    for (const w of Object.values(this.scene!.walls)) {
      const pts = w.closed ? [...w.points, w.points[0]] : w.points;
      for (let i = 0; i + 1 < pts.length; i++) if (distToSegment(p, pts[i], pts[i + 1]) < 0.2) return w.id;
    }
    return null;
  }

  // ---------- input ----------

  private bindEvents() {
    const cv = this.app.canvas;
    const on = <K extends keyof HTMLElementEventMap>(el: HTMLElement | Window, ev: K, f: (e: HTMLElementEventMap[K]) => void, opts?: AddEventListenerOptions) => {
      el.addEventListener(ev, f as EventListener, opts);
      this.cleanup.push(() => el.removeEventListener(ev, f as EventListener));
    };
    on(cv, "pointerdown", (e) => this.onDown(e));
    on(cv, "pointermove", (e) => this.onMove(e));
    on(cv, "pointerup", (e) => this.onUp(e));
    on(cv, "pointercancel", (e) => this.onUp(e));
    on(cv, "pointerleave", () => {
      this.hover = null;
      this.drawOverlay();
    });
    on(cv, "dblclick", (e) => {
      if (this.tool === "wall") return this.finishWall();
      if (this.tool !== "select" || !this.host.isDM || !this.scene) return;
      const p = this.canvasPoint(e);
      const tid = this.hitToken(this.toUnits(p.x, p.y));
      if (tid) this.host.editToken(tid);
    });
    on(cv, "contextmenu", (e) => e.preventDefault());
    on(cv, "wheel", (e) => {
      e.preventDefault();
      const p = this.canvasPoint(e);
      this.zoomAt(p.x, p.y, Math.exp(-e.deltaY * 0.0015));
      this.drawOverlay();
    }, { passive: false });
    on(cv, "dragover", (e) => {
      const types = e.dataTransfer?.types ?? [];
      if (!this.host.isDM) return;
      if (types.includes("application/x-tilefall-asset")) e.preventDefault();
      if (types.includes(TEMPLATE_MIME)) {
        e.preventDefault();
        const p = this.canvasPoint(e);
        this.dropHover = this.toUnits(p.x, p.y);
        this.drawOverlay();
      }
    });
    const clearDrop = () => {
      this.dropHover = null;
      this.drawOverlay();
    };
    on(cv, "dragleave", clearDrop);
    on(window, "dragend", clearDrop);
    on(cv, "drop", (e) => {
      if (!this.host.isDM) return;
      const p = this.canvasPoint(e);
      const at = this.toUnits(p.x, p.y);
      this.dropHover = null;
      const tpl = e.dataTransfer?.getData(TEMPLATE_MIME);
      if (tpl) {
        e.preventDefault();
        this.spawnToken(tpl, at);
        return;
      }
      const id = e.dataTransfer?.getData("application/x-tilefall-asset");
      if (!id) return;
      e.preventDefault();
      this.placeObject(id, at, e.shiftKey);
    });
    on(window, "keydown", (e) => this.onKey(e));
    on(window, "keyup", (e) => {
      if (e.key === "Shift") this.shift = false;
    });
  }

  private onDown(e: PointerEvent) {
    if (!this.scene) return;
    this.app.canvas.setPointerCapture(e.pointerId);
    const sp = this.canvasPoint(e);
    this.pointers.set(e.pointerId, sp);
    this.shift = e.shiftKey;
    if (this.pointers.size === 2) {
      const [a, b] = [...this.pointers.values()];
      this.gesture = { kind: "pinch", d0: Math.hypot(a.x - b.x, a.y - b.y), z0: this.zoom, mid: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } };
      return;
    }
    if (this.pointers.size > 2) return;
    const p = this.toUnits(sp.x, sp.y);
    const s = this.scene;
    const startPan = (clickClears: boolean) => {
      this.gesture = { kind: "pan", sx: sp.x, sy: sp.y, wx: this.world.x, wy: this.world.y, moved: false, clickClears };
    };
    if (e.button === 1 || e.button === 2) return startPan(false);

    switch (this.tool) {
      case "select": {
        const tid = this.hitToken(p);
        if (tid) {
          this.select({ kind: "token", id: tid });
          const cell = s.tokens[tid].cell;
          const path = new MovePath(s.grid, cell, cellCenter(s.grid, cell), e.timeStamp);
          this.gesture = { kind: "token", id: tid, cell, grab: p, moved: false, path };
          return;
        }
        if (this.host.isDM) {
          const oid = this.hitObject(p);
          if (oid) {
            const o = s.objects[oid];
            this.select({ kind: "object", id: oid });
            this.gesture = { kind: "object", id: oid, grab: { x: p.x - o.x, y: p.y - o.y }, pos: { x: o.x, y: o.y }, moved: false };
            return;
          }
          const wid = this.hitWall(p);
          if (wid) {
            this.select({ kind: "wall", id: wid });
            return;
          }
        }
        return startPan(true);
      }
      case "terrain":
      case "erase": {
        this.gesture = { kind: "paint", terrain: this.tool === "erase" ? "" : this.host.terrain, cells: new Map() };
        this.paintAt(p);
        return;
      }
      case "object":
        this.placeObject(this.host.assetId, p, e.shiftKey);
        return;
      case "wall":
        this.wallDraft.push(this.wallSnap(p));
        this.drawOverlay();
        return;
      case "token":
        this.spawnToken(this.host.templateId, p);
        return;
    }
  }

  private onMove(e: PointerEvent) {
    if (!this.scene) return;
    const sp = this.canvasPoint(e);
    if (this.pointers.has(e.pointerId)) this.pointers.set(e.pointerId, sp);
    this.shift = e.shiftKey;
    const p = this.toUnits(sp.x, sp.y);
    this.hover = p;
    const g = this.gesture;
    if (g?.kind === "pinch" && this.pointers.size === 2) {
      const [a, b] = [...this.pointers.values()];
      const d = Math.hypot(a.x - b.x, a.y - b.y);
      this.zoomAt(g.mid.x, g.mid.y, (g.z0 * d) / g.d0 / this.zoom);
    } else if (g?.kind === "pan") {
      const dx = sp.x - g.sx, dy = sp.y - g.sy;
      if (Math.abs(dx) + Math.abs(dy) > 3) g.moved = true;
      this.world.position.set(g.wx + dx, g.wy + dy);
    } else if (g?.kind === "token") {
      // move the token's anchor cell by the drag offset, snapping to cells
      const s = this.scene;
      const c0 = cellCenter(s.grid, s.tokens[g.id].cell);
      // coalesced events give the path every sample, which the corner-cut timing and angle need
      const coalesced = e.getCoalescedEvents?.();
      for (const ev of coalesced?.length ? coalesced : [e]) {
        const cp = this.canvasPoint(ev);
        const u = this.toUnits(cp.x, cp.y);
        const q = { x: c0.x + u.x - g.grab.x, y: c0.y + u.y - g.grab.y };
        const cell = pointToCell(s.grid, q);
        if (!inBounds(s.width, s.height, cell)) continue;
        g.path.add(cell, q, ev.timeStamp);
        if (cell.col !== g.cell.col || cell.row !== g.cell.row) g.moved = true;
        g.cell = cell;
      }
      this.syncTokens();
    } else if (g?.kind === "object") {
      let pos = { x: p.x - g.grab.x, y: p.y - g.grab.y };
      if (e.shiftKey) pos = cellCenter(this.scene.grid, pointToCell(this.scene.grid, pos));
      g.pos = pos;
      g.moved = true;
      const o = this.scene.objects[g.id];
      const sprite = this.sprites.get(g.id);
      if (o && sprite) this.placeSprite(sprite, o, pos);
    } else if (g?.kind === "paint") {
      this.paintAt(p);
    }
    this.drawOverlay();
  }

  private onUp(e: PointerEvent) {
    this.pointers.delete(e.pointerId);
    const g = this.gesture;
    if (!g || !this.scene) return;
    if (g.kind === "pinch") {
      if (this.pointers.size === 0) this.gesture = null;
      return;
    }
    this.gesture = null;
    const s = this.scene;
    if (g.kind === "pan") {
      if (!g.moved && g.clickClears) this.select(null);
    } else if (g.kind === "token") {
      const t = s.tokens[g.id];
      if (t && g.moved && (g.cell.col !== t.cell.col || g.cell.row !== t.cell.row)) {
        s.tokens[g.id] = { ...t, cell: g.cell };
        this.host.touched();
        this.host.send({ type: "move_token", id: g.id, cell: g.cell });
      }
      this.syncTokens();
    } else if (g.kind === "object") {
      const o = s.objects[g.id];
      if (o && g.moved) {
        const x = round3(g.pos.x), y = round3(g.pos.y);
        s.objects[g.id] = { ...o, x, y };
        this.host.touched();
        this.host.send({ type: "update_object", id: g.id, x, y });
      }
    } else if (g.kind === "paint") {
      if (g.cells.size) this.host.send({ type: "fill_terrain", region: { cells: [...g.cells.values()] }, terrain: g.terrain });
    }
    this.drawOverlay();
  }

  private paintAt(p: Point) {
    const g = this.gesture;
    if (g?.kind !== "paint") return;
    const s = this.scene!;
    const changed: string[] = [];
    for (const c of this.brushCells(pointToCell(s.grid, p))) {
      const k = cellKey(c);
      if (g.cells.has(k)) continue;
      g.cells.set(k, c);
      // optimistic: draw immediately, the server patch confirms it
      if (g.terrain) s.terrain[k] = g.terrain;
      else delete s.terrain[k];
      changed.push(k);
    }
    if (changed.length) this.drawTerrainKeys(changed);
  }

  /** Anchor cell for a size-n token centered on p (multi-cell tokens anchor top-left). */
  private anchorAt(p: Point, size: number): Cell {
    const off = (size - 1) / 2;
    return pointToCell(this.scene!.grid, { x: p.x - off, y: p.y - off });
  }

  private templateSize(id: string) {
    return (id && this.host.template(id)?.size) || 1;
  }

  /** Add a token centered on p, from a library template or ("" templateId) blank. */
  private spawnToken(templateId: string, p: Point) {
    const s = this.scene;
    if (!s) return;
    const cell = this.anchorAt(p, this.templateSize(templateId));
    if (!inBounds(s.width, s.height, cell)) return;
    const cmd: Command = templateId
      ? { type: "add_token", templateId, cell }
      : { type: "add_token", name: `Token ${Object.keys(s.tokens).length + 1}`, cell };
    this.host.send(cmd).then((a) => {
      const id = (a.result as { id?: string } | undefined)?.id;
      if (a.ok && id) this.select({ kind: "token", id });
    });
  }

  private placeObject(assetId: string, p: Point, snap: boolean) {
    const s = this.scene;
    if (!s) return;
    const spec: Record<string, unknown> = { assetId };
    if (snap) spec.cell = pointToCell(s.grid, p);
    else Object.assign(spec, { x: round3(p.x), y: round3(p.y) });
    this.host.send({ type: "place_objects", objects: [spec] });
  }

  private finishWall() {
    if (this.tool !== "wall") return;
    // a double-click adds the same point twice; drop consecutive duplicates
    const pts = this.wallDraft.filter((p, i, a) => i === 0 || p.x !== a[i - 1].x || p.y !== a[i - 1].y);
    this.wallDraft = [];
    if (pts.length >= 2) this.host.send({ type: "add_walls", walls: [{ points: pts, style: this.host.wallStyle }] });
    this.drawOverlay();
  }

  private select(sel: Selection) {
    this.host.selection = sel;
    this.drawOverlay();
  }

  private onKey(e: KeyboardEvent) {
    const target = e.target as HTMLElement | null;
    if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable || target.closest("dialog"))) return;
    if (e.key === "Shift") this.shift = true;
    const s = this.scene;
    if (!s || !this.host.isDM) return;
    const sel = this.host.selection;
    if (e.key === "Escape") {
      this.wallDraft = [];
      this.select(null);
    } else if (e.key === "Enter") {
      this.finishWall();
    } else if ((e.key === "Delete" || e.key === "Backspace") && sel) {
      e.preventDefault();
      const type = { object: "remove_objects", token: "remove_tokens", wall: "remove_walls" }[sel.kind];
      this.host.send({ type, ids: [sel.id] });
      this.select(null);
    } else if (sel?.kind === "object" && s.objects[sel.id]) {
      const o = s.objects[sel.id];
      if (e.key === "r" || e.key === "R") this.host.send({ type: "update_object", id: o.id, rotation: o.rotation + (e.shiftKey ? -45 : 45) });
      else if (e.key === "]") this.host.send({ type: "update_object", id: o.id, scale: round3(o.scale * 1.1) });
      else if (e.key === "[") this.host.send({ type: "update_object", id: o.id, scale: round3(o.scale / 1.1) });
    }
  }
}

const round3 = (v: number) => Math.round(v * 1000) / 1000;

function distToSegment(p: Point, a: Point, b: Point) {
  const dx = b.x - a.x, dy = b.y - a.y;
  const l2 = dx * dx + dy * dy;
  const t = l2 === 0 ? 0 : Math.max(0, Math.min(1, ((p.x - a.x) * dx + (p.y - a.y) * dy) / l2));
  return Math.hypot(p.x - (a.x + t * dx), p.y - (a.y + t * dy));
}
