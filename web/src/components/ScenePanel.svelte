<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { gridScale, mapExtent } from "../lib/grid";
  import type { Command, GridType, HexOrientation } from "../lib/types";

  let { send, fit }: { send: (c: Command) => Promise<unknown>; fit: () => void } = $props();

  interface SceneInfo {
    id: string;
    name: string;
    width: number;
    height: number;
    grid: string;
  }

  let scenes = $state<SceneInfo[]>([]);
  let showNew = $state(false);
  let draft = $state({ name: "New map", width: 30, height: 20, gridType: "square" as GridType, hexOrientation: "pointy" as HexOrientation, terrain: "" });

  const s = $derived.by(() => {
    app.rev;
    return app.scene ? { ...app.scene, grid: { ...app.scene.grid } } : null;
  });

  async function refreshScenes() {
    const r = await fetch("/api/scenes").then((r) => r.json());
    scenes = r.scenes ?? [];
  }

  async function post(url: string, body?: unknown) {
    const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    const data = await r.json();
    if (!r.ok) app.toast(data.error ?? "request failed");
    return r.ok;
  }

  async function createScene() {
    if (await post("/api/scenes", draft)) {
      showNew = false;
      refreshScenes();
    }
  }

  async function loadScene(id: string) {
    if (await post(`/api/scenes/${id}/load`)) refreshScenes();
  }

  async function uploadBackground(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    if (!file || !app.scene) return;
    const form = new FormData();
    form.append("file", file);
    const r = await fetch("/api/uploads", { method: "POST", body: form });
    const data = await r.json();
    input.value = "";
    if (!r.ok) return app.toast(data.error ?? "upload failed");
    // stretch to cover the map, keeping the image's aspect ratio
    const img = new Image();
    img.src = data.url;
    await img.decode();
    const ext = mapExtent(app.scene.grid, app.scene.width, app.scene.height);
    const scale = Math.max(ext.x / img.naturalWidth, ext.y / img.naturalHeight);
    send({ type: "update_scene", background: { url: data.url, x: 0, y: 0, width: img.naturalWidth * scale, height: img.naturalHeight * scale } });
  }

  // the Vite dev server proxies everything except MCP, which lives on the Go server
  const serverPort = location.port === "5173" ? "8080" : location.port || "80";

  const num = (e: Event) => Number((e.currentTarget as HTMLInputElement).value);
  const str = (e: Event) => (e.currentTarget as HTMLInputElement).value;
  const grid = (g: Record<string, unknown>) => send({ type: "update_scene", grid: g });

  $effect(() => {
    app.scene?.id;
    refreshScenes();
  });
</script>

{#if s}
  <section>
    <h3>Scene</h3>
    <label class="row">Name <input type="text" value={s.name} onchange={(e) => send({ type: "update_scene", name: str(e) })} /></label>
    <label class="row">Size
      <input type="number" min="1" max="200" value={s.width} onchange={(e) => send({ type: "update_scene", width: num(e) })} />
      ×
      <input type="number" min="1" max="200" value={s.height} onchange={(e) => send({ type: "update_scene", height: num(e) })} />
    </label>
    <label class="row">Grid
      <select value={s.grid.type} onchange={(e) => grid({ type: str(e) })}>
        <option value="square">Square</option>
        <option value="hex">Hex</option>
      </select>
      {#if s.grid.type === "hex"}
        <select value={s.grid.hexOrientation} onchange={(e) => grid({ hexOrientation: str(e) })}>
          <option value="pointy">Pointy top</option>
          <option value="flat">Flat top</option>
        </select>
      {/if}
    </label>
    <label class="row">Cell px <input type="number" min="10" max="500" value={s.grid.cellSize} onchange={(e) => grid({ cellSize: num(e) })} /></label>
    <label class="row" title="How far one cell is when measuring moves. Diagonals cost the same as straight moves.">1 cell =
      <input type="number" min="0.5" max="10000" step="0.5" value={gridScale(s.grid).per} onchange={(e) => grid({ distance: num(e) })} />
      <input type="text" maxlength="12" size="4" value={gridScale(s.grid).units} onchange={(e) => grid({ units: str(e) })} />
    </label>
    <label class="row">Lines
      <input type="checkbox" checked={s.grid.visible} onchange={(e) => grid({ visible: (e.currentTarget as HTMLInputElement).checked })} />
      <input type="color" value={s.grid.color} onchange={(e) => grid({ color: str(e) })} />
      <input type="range" min="0" max="1" step="0.05" value={s.grid.opacity} onchange={(e) => grid({ opacity: num(e) })} />
    </label>
    <label class="row">Background <input type="file" accept="image/*" onchange={uploadBackground} /></label>
    {#if s.background}
      <button onclick={() => send({ type: "update_scene", clearBackground: true })}>Remove background</button>
    {/if}
    <button onclick={fit}>Fit to screen</button>
  </section>
{/if}

<section>
  <h3>Scenes</h3>
  {#each scenes as sc}
    <div class="scene" class:current={sc.id === app.scene?.id}>
      <span>{sc.name} <small>{sc.width}×{sc.height} {sc.grid}</small></span>
      {#if sc.id !== app.scene?.id}<button onclick={() => loadScene(sc.id)}>Load</button>{/if}
    </div>
  {/each}
  {#if showNew}
    <div class="new">
      <label class="row">Name <input type="text" bind:value={draft.name} /></label>
      <label class="row">Size <input type="number" min="1" max="200" bind:value={draft.width} /> × <input type="number" min="1" max="200" bind:value={draft.height} /></label>
      <label class="row">Grid
        <select bind:value={draft.gridType}><option value="square">Square</option><option value="hex">Hex</option></select>
        {#if draft.gridType === "hex"}
          <select bind:value={draft.hexOrientation}><option value="pointy">Pointy</option><option value="flat">Flat</option></select>
        {/if}
      </label>
      <label class="row">Fill
        <select bind:value={draft.terrain}>
          <option value="">(none)</option>
          {#each app.catalog?.terrains ?? [] as t}<option value={t.id}>{t.name}</option>{/each}
        </select>
      </label>
      <button onclick={createScene}>Create &amp; switch</button>
      <button onclick={() => (showNew = false)}>Cancel</button>
    </div>
  {:else}
    <button onclick={() => (showNew = true)}>New scene…</button>
  {/if}
</section>

<section>
  <h3>Agent access</h3>
  <p class="hint">Let Claude Code build maps and run the table:</p>
  <code>claude mcp add --transport http tilefall http://localhost:{serverPort}/mcp</code>
</section>

<style>
  .scene {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 3px 0;
    font-size: 13px;
  }
  .scene.current {
    color: var(--accent);
  }
  .new {
    border: 1px solid var(--border);
    padding: 8px;
    border-radius: 6px;
  }
  code {
    display: block;
    font-size: 11px;
    background: #111;
    padding: 6px;
    border-radius: 4px;
    word-break: break-all;
    user-select: all;
  }
</style>
