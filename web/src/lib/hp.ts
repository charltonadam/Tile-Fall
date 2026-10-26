/** Health bar color for a 0..1 fraction of max hp; shared by the map and the sidebar. */
export function hpColor(frac: number): string {
  if (frac > 0.5) return "#2ecc71";
  if (frac > 0.25) return "#f1c40f";
  return "#e74c3c";
}
