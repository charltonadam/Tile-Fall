<script lang="ts">
  import { app } from "../lib/state.svelte";
  import type { Command } from "../lib/types";
  import Inspector from "./Inspector.svelte";
  import ScenePanel from "./ScenePanel.svelte";
  import TokenTray from "./TokenTray.svelte";

  let { send, fit }: { send: (c: Command) => Promise<unknown>; fit: () => void } = $props();

  const cat = $derived(app.catalog);

  // object palette filter: free text matches name, id and tags
  let assetQuery = $state("");
  let assetTag = $state("");
  const assetTags = $derived([...new Set(cat?.assets.flatMap((a) => a.tags) ?? [])].sort());
  const shownAssets = $derived.by(() => {
    const q = assetQuery.trim().toLowerCase();
    return (cat?.assets ?? []).filter(
      (a) =>
        (!assetTag || a.tags.includes(assetTag)) &&
        (!q || a.name.toLowerCase().includes(q) || a.id.includes(q) || a.tags.some((t) => t.includes(q))),
    );
  });

  // selecting something on the map brings back the inspector
  $effect(() => {
    if (app.selection) app.scenePanel = false;
  });

  function dragAsset(e: DragEvent, id: string) {
    e.dataTransfer?.setData("application/x-tilefall-asset", id);
    app.assetId = id;
  }
</script>

<aside>
  {#if app.scenePanel}
    <ScenePanel {send} {fit} />
  {:else if cat}
    {#if app.tool === "terrain" || app.tool === "erase"}
      <section>
        <h3>{app.tool === "erase" ? "Erase terrain" : "Terrain"}</h3>
        {#if app.tool === "terrain"}
          <div class="swatches">
            {#each cat.terrains as t}
              <button class="swatch" class:active={app.terrain === t.id} title={t.description} onclick={() => (app.terrain = t.id)}>
                <span class="chip" style:background={t.color}></span>{t.name}
              </button>
            {/each}
          </div>
        {/if}
        <label class="row">Brush <input type="range" min="1" max="6" bind:value={app.brush} /> {app.brush}</label>
        {#if app.tool === "terrain"}
          <button onclick={() => send({ type: "fill_terrain", region: { all: true }, terrain: app.terrain })}>Fill whole map</button>
        {:else}
          <button onclick={() => send({ type: "fill_terrain", region: { all: true }, terrain: "" })}>Erase whole map</button>
        {/if}
      </section>
    {:else if app.tool === "object"}
      <section>
        <h3>Objects</h3>
        <p class="hint">Click the map to place, or drag onto it. Hold Shift to snap to a cell.</p>
        <div class="row">
          <input type="text" placeholder="Search objects" bind:value={assetQuery} />
          <select bind:value={assetTag} title="Filter by tag">
            <option value="">All</option>
            {#each assetTags as t}
              <option value={t}>{t}</option>
            {/each}
          </select>
        </div>
        <div class="assets">
          {#each shownAssets as a (a.id)}
            <button
              class="asset"
              class:active={app.assetId === a.id}
              title="{a.name}: {a.description}"
              draggable="true"
              ondragstart={(e) => dragAsset(e, a.id)}
              onclick={() => (app.assetId = a.id)}
            >
              <img src="/assets/{a.file}" alt={a.name} draggable="false" />
              <span>{a.name}</span>
            </button>
          {:else}
            <p class="hint">No objects match.</p>
          {/each}
        </div>
      </section>
    {:else if app.tool === "wall"}
      <section>
        <h3>Walls</h3>
        <p class="hint">Click to add points (they snap to cell corners; hold Shift for free placement). Double-click or Enter to finish, Esc to cancel.</p>
        {#each cat.wallStyles as w}
          <button class="swatch" class:active={app.wallStyle === w.id} onclick={() => (app.wallStyle = w.id)}>
            <span class="chip" style:background={w.color}></span>{w.name}
          </button>
        {/each}
      </section>
    {:else if app.tool === "token"}
      <section>
        <p class="hint">Click a cell to place the highlighted template{app.templateId ? "" : " (or a blank token if none is highlighted)"}.</p>
      </section>
      <TokenTray />
    {:else}
      <section>
        <p class="hint">Drag tokens and objects to move them. Double-click a token to edit it. Select an object and press R to rotate, [ and ] to resize, Delete to remove. Drag empty space to pan; scroll to zoom.</p>
      </section>
      <TokenTray />
    {/if}
  {/if}

  {#if !app.scenePanel}
    <Inspector {send} />
  {/if}
</aside>

<style>
  aside {
    grid-area: side;
    overflow-y: auto;
    overflow-x: hidden;
    background: var(--panel);
    border-left: 1px solid var(--border);
    padding: 8px 12px 24px;
  }
  .swatches {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 4px;
    margin-bottom: 8px;
  }
  .swatch {
    display: flex;
    align-items: center;
    gap: 6px;
    text-align: left;
    font-size: 12px;
    width: 100%;
    margin-bottom: 4px;
  }
  .chip {
    display: inline-block;
    width: 14px;
    height: 14px;
    border-radius: 3px;
    border: 1px solid #0006;
    flex: none;
  }
  .assets {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 6px;
  }
  .asset {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 4px;
    font-size: 11px;
    cursor: grab;
  }
  .assets .hint {
    grid-column: 1 / -1;
  }
  .asset img {
    width: 48px;
    height: 48px;
  }
  .active {
    border-color: var(--accent);
    background: #3a3420;
  }
</style>
