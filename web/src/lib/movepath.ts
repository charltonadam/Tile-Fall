// Tracks the cells a token is dragged through and the movement they cost.
//
// The hard part is diagonals. Moving to a diagonal neighbor means passing
// exactly through a cell corner, which no one hits by hand: the pointer clips
// one of the two cells beside the corner for a moment, and counting that cell
// would turn a 5 ft diagonal into a 10 ft L. So when the path goes A -> B -> C
// with A and C adjacent and B touching both (B only "clipped the corner"
// shared by all three), we decide whether B was intended, using three signals:
//
//   - time:   the pointer left B again quickly
//   - corner: every sample inside B stayed close to the shared corner
//   - angle:  the pointer was heading from A toward C both when it entered B
//             and when it left it (a deliberate L turns in between)
//
// Any two of the three drop B. This works on hex grids too, where cutting
// across the vertex shared by three hexes has the same problem.
//
// Points are in grid units, in the space of the token's anchor cell (the
// pointer position minus the grab offset), so multi-cell tokens work too.

import { cellCenter, cellCorners, steps } from "./grid";
import type { Cell, Grid, Point } from "./types";

export const CORNER_CUT = {
  /** pointer left the clipped cell within this many ms */
  maxDwellMs: 200,
  /** samples in the clipped cell stayed within this many cells of the shared corner */
  cornerRadius: 0.3,
  /** heading in and out of the clipped cell within this many degrees of the A->C direction */
  maxAngleDeg: 30,
  /** measure headings over at least this much pointer travel, to ignore pixel jitter */
  lookback: 0.2,
};

const MAX_SAMPLES = 64;

interface Sample {
  p: Point;
  t: number;
}

interface Entry {
  cell: Cell;
  /** when the pointer entered this cell */
  t: number;
  samples: Sample[];
  /** samples were dropped (a long stay), so the corner test can't trust them */
  trimmed: boolean;
}

const same = (a: Cell, b: Cell) => a.col === b.col && a.row === b.row;
const dist = (a: Point, b: Point) => Math.hypot(a.x - b.x, a.y - b.y);

export class MovePath {
  private entries: Entry[];

  constructor(private grid: Grid, start: Cell, p: Point, t: number) {
    this.entries = [{ cell: start, t, samples: [{ p, t }], trimmed: false }];
  }

  get cells(): Cell[] {
    return this.entries.map((e) => e.cell);
  }

  /** Total single-cell moves along the path. */
  get steps(): number {
    let n = 0;
    for (let i = 1; i < this.entries.length; i++) n += steps(this.grid, this.entries[i - 1].cell, this.entries[i].cell);
    return n;
  }

  /** Record a pointer sample at p, which lies in `cell`. Returns true if the path changed. */
  add(cell: Cell, p: Point, t: number): boolean {
    const last = this.entries[this.entries.length - 1];
    if (same(last.cell, cell)) {
      push(last, { p, t });
      return false;
    }
    // dragging back over the path retraces it instead of adding distance
    const back = this.entries.findIndex((e) => same(e.cell, cell));
    if (back >= 0) {
      this.entries.length = back + 1;
      this.entries[back] = { cell, t, samples: [{ p, t }], trimmed: false };
      return true;
    }
    const next: Entry = { cell, t, samples: [{ p, t }], trimmed: false };
    const n = this.entries.length;
    if (n >= 2 && this.clippedCorner(this.entries[n - 2], last, next)) this.entries.pop();
    this.entries.push(next);
    return true;
  }

  /** Whether b was only clipped on the way from a to c (see the top of the file). */
  private clippedCorner(a: Entry, b: Entry, c: Entry): boolean {
    const g = this.grid;
    if (steps(g, a.cell, c.cell) !== 1 || steps(g, a.cell, b.cell) !== 1 || steps(g, b.cell, c.cell) !== 1) return false;
    const corner = sharedCorner(g, [a.cell, b.cell, c.cell]);
    if (!corner) return false;

    const fast = c.t - b.t <= CORNER_CUT.maxDwellMs;
    const near = !b.trimmed && b.samples.every((s) => dist(s.p, corner) <= CORNER_CUT.cornerRadius);

    const ca = cellCenter(g, a.cell), cc = cellCenter(g, c.cell);
    const want = Math.atan2(cc.y - ca.y, cc.x - ca.x);
    const hIn = heading(b.samples[0].p, a.samples);
    const hOut = heading(c.samples[0].p, [...a.samples, ...b.samples]);
    const aligned = hIn !== null && hOut !== null && angleDiff(hIn, want) <= CORNER_CUT.maxAngleDeg && angleDiff(hOut, want) <= CORNER_CUT.maxAngleDeg;

    return Number(fast) + Number(near) + Number(aligned) >= 2;
  }
}

function push(e: Entry, s: Sample) {
  e.samples.push(s);
  if (e.samples.length > MAX_SAMPLES) {
    e.samples.shift();
    e.trimmed = true;
  }
}

/** Direction of travel arriving at `to`, measured from the latest earlier sample at least `lookback` away. */
function heading(to: Point, before: Sample[]): number | null {
  let from: Point | null = null;
  for (let i = before.length - 1; i >= 0; i--) {
    from = before[i].p;
    if (dist(from, to) >= CORNER_CUT.lookback) break;
  }
  if (!from || dist(from, to) < 0.02) return null;
  return Math.atan2(to.y - from.y, to.x - from.x);
}

/** Absolute difference between two angles in radians, in degrees (0..180). */
function angleDiff(a: number, b: number): number {
  const d = Math.abs(((a - b) * 180) / Math.PI) % 360;
  return d > 180 ? 360 - d : d;
}

/** The cell corner that all the given cells share, if any. */
export function sharedCorner(g: Grid, cells: Cell[]): Point | null {
  const [first, ...rest] = cells.map((c) => cellCorners(g, c));
  return first.find((k) => rest.every((ks) => ks.some((q) => dist(q, k) < 1e-6))) ?? null;
}
