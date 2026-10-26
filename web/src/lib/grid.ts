// Mirror of internal/scene/grid.go. Keep the two in sync; grid.test.ts checks
// this file against internal/scene/testdata/grid_vectors.json.
//
// World coordinates are grid units (1 = one cell). Cells are (col,row); hex
// grids use odd-r offset (pointy) or odd-q offset (flat) coordinates.

import type { Cell, Grid, Point } from "./types";

const SQRT3 = Math.sqrt(3);
const HEX_R = 1 / SQRT3;

const isHex = (g: Grid) => g.type === "hex";
const isFlat = (g: Grid) => g.type === "hex" && g.hexOrientation === "flat";

export const cellKey = (c: Cell) => `${c.col},${c.row}`;

export function parseCellKey(k: string): Cell {
  const [col, row] = k.split(",").map(Number);
  return { col, row };
}

export function cellCenter(g: Grid, c: Cell): Point {
  if (isFlat(g)) return { x: 1.5 * HEX_R * c.col + HEX_R, y: c.row + 0.5 * (c.col & 1) + 0.5 };
  if (isHex(g)) return { x: c.col + 0.5 * (c.row & 1) + 0.5, y: 1.5 * HEX_R * c.row + HEX_R };
  return { x: c.col + 0.5, y: c.row + 0.5 };
}

export function pointToCell(g: Grid, p: Point): Cell {
  if (!isHex(g)) return { col: Math.floor(p.x), row: Math.floor(p.y) };
  let q: number, r: number;
  if (isFlat(g)) {
    const px = p.x - HEX_R, py = p.y - 0.5;
    q = ((2 / 3) * px) / HEX_R;
    r = ((-1 / 3) * px + (SQRT3 / 3) * py) / HEX_R;
  } else {
    const px = p.x - 0.5, py = p.y - HEX_R;
    q = ((SQRT3 / 3) * px - (1 / 3) * py) / HEX_R;
    r = ((2 / 3) * py) / HEX_R;
  }
  const [aq, ar] = axialRound(q, r);
  if (isFlat(g)) return { col: aq, row: ar + (aq - (aq & 1)) / 2 };
  return { col: aq + (ar - (ar & 1)) / 2, row: ar };
}

// Go's math.Round rounds half away from zero; Math.round rounds half up.
const roundHalfAway = (v: number) => Math.sign(v) * Math.round(Math.abs(v));

function axialRound(q: number, r: number): [number, number] {
  const s = -q - r;
  let rq = roundHalfAway(q), rr = roundHalfAway(r);
  const rs = roundHalfAway(s);
  const dq = Math.abs(rq - q), dr = Math.abs(rr - r), ds = Math.abs(rs - s);
  if (dq > dr && dq > ds) rq = -rr - rs;
  else if (dr > ds) rr = -rq - rs;
  return [rq + 0, rr + 0]; // + 0 turns -0 into 0
}

export function cellCorners(g: Grid, c: Cell): Point[] {
  const ctr = cellCenter(g, c);
  if (!isHex(g)) {
    return [
      { x: ctr.x - 0.5, y: ctr.y - 0.5 },
      { x: ctr.x + 0.5, y: ctr.y - 0.5 },
      { x: ctr.x + 0.5, y: ctr.y + 0.5 },
      { x: ctr.x - 0.5, y: ctr.y + 0.5 },
    ];
  }
  const start = isFlat(g) ? 0 : -30;
  const pts: Point[] = [];
  for (let i = 0; i < 6; i++) {
    const a = ((start + 60 * i) * Math.PI) / 180;
    pts.push({ x: ctr.x + HEX_R * Math.cos(a), y: ctr.y + HEX_R * Math.sin(a) });
  }
  return pts;
}

export function mapExtent(g: Grid, w: number, h: number): Point {
  if (isFlat(g)) return { x: 1.5 * HEX_R * (w - 1) + 2 * HEX_R, y: h + 0.5 };
  if (isHex(g)) return { x: w + 0.5, y: 1.5 * HEX_R * (h - 1) + 2 * HEX_R };
  return { x: w, y: h };
}

export const inBounds = (w: number, h: number, c: Cell) => c.col >= 0 && c.row >= 0 && c.col < w && c.row < h;

/** Center of a token: multi-cell tokens extend right/down from their anchor cell (matches internal/render). */
export function tokenCenter(g: Grid, cell: Cell, size: number): Point {
  const c = cellCenter(g, cell);
  const off = (size - 1) / 2;
  return { x: c.x + off, y: c.y + off };
}

/** Cells whose centers are within radius of the center of `c`, like a scene.Region circle. */
export function cellsInRadius(g: Grid, w: number, h: number, c: Cell, radius: number): Cell[] {
  const ctr = cellCenter(g, c);
  const out: Cell[] = [];
  const span = Math.ceil(radius) + 1;
  for (let row = c.row - span; row <= c.row + span; row++) {
    for (let col = c.col - span; col <= c.col + span; col++) {
      const cell = { col, row };
      if (!inBounds(w, h, cell)) continue;
      const p = cellCenter(g, cell);
      if (Math.hypot(p.x - ctr.x, p.y - ctr.y) <= radius + 1e-6) out.push(cell);
    }
  }
  return out;
}

/** Nearest cell corner to p, used to snap wall points. */
export function nearestCorner(g: Grid, p: Point): Point {
  const cell = pointToCell(g, p);
  let best = p, bd = Infinity;
  for (const k of cellCorners(g, cell)) {
    const d = Math.hypot(k.x - p.x, k.y - p.y);
    if (d < bd) [best, bd] = [k, d];
  }
  return best;
}

/** Distance per cell and unit label, with the D&D defaults (5 ft) for scenes that don't set them. */
export function gridScale(g: Grid): { per: number; units: string } {
  return { per: g.distance && g.distance > 0 ? g.distance : 5, units: g.units || "ft" };
}

/**
 * Single-cell moves between a and b. Square grids count diagonals the same as
 * straight moves (D&D 5e), so this is Chebyshev distance; hex uses hex distance.
 */
export function steps(g: Grid, a: Cell, b: Cell): number {
  if (!isHex(g)) return Math.max(Math.abs(a.col - b.col), Math.abs(a.row - b.row));
  const [aq, ar] = toAxial(g, a), [bq, br] = toAxial(g, b);
  const dq = aq - bq, dr = ar - br;
  return (Math.abs(dq) + Math.abs(dr) + Math.abs(dq + dr)) / 2;
}

function toAxial(g: Grid, c: Cell): [number, number] {
  if (isFlat(g)) return [c.col, c.row - (c.col - (c.col & 1)) / 2];
  return [c.col - (c.row - (c.row & 1)) / 2, c.row];
}
