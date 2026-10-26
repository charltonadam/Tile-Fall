import { describe, expect, it } from "vitest";
import { cellCenter, pointToCell, steps } from "./grid";
import { MovePath, sharedCorner } from "./movepath";
import type { Cell, Grid, Point } from "./types";

const square: Grid = { type: "square", cellSize: 70, visible: true, color: "#000000", opacity: 1 };
const hex: Grid = { type: "hex", hexOrientation: "pointy", cellSize: 70, visible: true, color: "#000000", opacity: 1 };

/** Drag along straight segments through `via`, sampling every `stepUnits` and `dtMs` apart. */
function drag(g: Grid, via: Point[], dtMs: number, stepUnits = 0.04) {
  const path = new MovePath(g, pointToCell(g, via[0]), via[0], 0);
  let t = 0;
  for (let i = 1; i < via.length; i++) {
    const a = via[i - 1], b = via[i];
    const n = Math.max(1, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y) / stepUnits));
    for (let k = 1; k <= n; k++) {
      const p = { x: a.x + ((b.x - a.x) * k) / n, y: a.y + ((b.y - a.y) * k) / n };
      t += dtMs;
      path.add(pointToCell(g, p), p, t);
    }
  }
  return path;
}

const cells = (p: MovePath) => p.cells.map((c) => `${c.col},${c.row}`);

describe("square diagonals", () => {
  // a diagonal stroke that misses the corner by 0.1 cells clips cell 1,0
  const sloppyDiagonal = [{ x: 0.6, y: 0.5 }, { x: 1.6, y: 1.5 }];

  it("drops a corner clipped during a quick diagonal", () => {
    const p = drag(square, sloppyDiagonal, 8);
    expect(cells(p)).toEqual(["0,0", "1,1"]);
    expect(p.steps).toBe(1);
  });

  it("drops it on a slow diagonal too (near the corner and heading diagonally)", () => {
    const p = drag(square, sloppyDiagonal, 80);
    expect(cells(p)).toEqual(["0,0", "1,1"]);
  });

  it("drops it on a quick diagonal that passes farther from the corner", () => {
    const p = drag(square, [{ x: 0.75, y: 0.4 }, { x: 1.75, y: 1.4 }], 8);
    expect(cells(p)).toEqual(["0,0", "1,1"]);
  });

  it("keeps a deliberate L, even a quick one", () => {
    for (const dt of [8, 80]) {
      const p = drag(square, [{ x: 0.5, y: 0.5 }, { x: 1.5, y: 0.5 }, { x: 1.5, y: 1.5 }], dt);
      expect(cells(p)).toEqual(["0,0", "1,0", "1,1"]);
      expect(p.steps).toBe(2);
    }
  });

  it("keeps a cell that was lingered in near the corner while not heading diagonally", () => {
    // straight right to just past the corner, wait, then straight down
    const p = drag(square, [{ x: 0.5, y: 0.8 }, { x: 1.1, y: 0.8 }, { x: 1.1, y: 0.8 }, { x: 1.1, y: 0.8 }, { x: 1.1, y: 1.3 }], 120, 0.05);
    expect(cells(p)).toEqual(["0,0", "1,0", "1,1"]);
  });

  it("chains several diagonals in a row", () => {
    const p = drag(square, [{ x: 0.6, y: 0.5 }, { x: 3.6, y: 3.5 }], 8);
    expect(cells(p)).toEqual(["0,0", "1,1", "2,2", "3,3"]);
    expect(p.steps).toBe(3);
  });

  it("still retraces when dragging back", () => {
    const p = drag(square, [{ x: 0.5, y: 0.5 }, { x: 3.5, y: 0.5 }, { x: 1.5, y: 0.5 }], 30);
    expect(cells(p)).toEqual(["0,0", "1,0"]);
  });
});

describe("hex corner cuts", () => {
  // A and C are neighbors; B touches both, so A -> B -> C costs 2 but A -> C costs 1
  const A: Cell = { col: 3, row: 3 };
  const C: Cell = { col: 4, row: 3 };
  const B = [{ col: 3, row: 2 }, { col: 4, row: 2 }, { col: 3, row: 4 }, { col: 4, row: 4 }].find(
    (b) => steps(hex, A, b) === 1 && steps(hex, C, b) === 1,
  )!;
  const k = sharedCorner(hex, [A, B, C])!;
  const ca = cellCenter(hex, A), cb = cellCenter(hex, B), cc = cellCenter(hex, C);

  it("drops a hex that was clipped near the shared vertex", () => {
    // aim at a point just inside B next to the vertex, then on to C's center
    const nick = { x: k.x + (cb.x - k.x) * 0.15, y: k.y + (cb.y - k.y) * 0.15 };
    const p = drag(hex, [ca, nick, cc], 8, 0.03);
    expect(cells(p)).toEqual([`${A.col},${A.row}`, `${C.col},${C.row}`]);
    expect(p.steps).toBe(1);
  });

  it("keeps a hex that was actually walked through", () => {
    const p = drag(hex, [ca, cb, cc], 60);
    expect(cells(p)).toEqual([A, B, C].map((c) => `${c.col},${c.row}`));
    expect(p.steps).toBe(2);
  });
});
