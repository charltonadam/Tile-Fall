<script lang="ts">
  import { app } from "../lib/state.svelte";
  import TokenChip from "./TokenChip.svelte";

  function dragTemplate(e: DragEvent, id: string) {
    e.dataTransfer?.setData("application/x-tilefall-template", id);
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "copy";
    // dragover can't read the payload, so the map reads the size from here
    app.templateId = id;
  }
</script>

<section>
  <h3>Token library</h3>
  <p class="hint">Drag onto the map to spawn at full HP. Double-click to edit the defaults; tokens already on the map don't change.</p>
  <div class="tray">
    {#each app.library as t (t.id)}
      <button
        class="tpl"
        class:active={app.templateId === t.id}
        title="{t.name}{t.maxHp ? ` · ${t.maxHp} HP` : ''}"
        draggable="true"
        ondragstart={(e) => dragTemplate(e, t.id)}
        onclick={() => (app.templateId = t.id)}
        ondblclick={() => (app.editing = { kind: "template", id: t.id })}
      >
        <TokenChip name={t.name} color={t.color} imageUrl={t.imageUrl} px={36} />
        <span class="name">{t.name}</span>
        {#if t.maxHp}<small>{t.maxHp} HP</small>{/if}
      </button>
    {/each}
    <button class="tpl add" title="New template" onclick={() => (app.editing = { kind: "template", id: "" })}>
      <span class="plus">+</span>
      <span class="name">New</span>
    </button>
  </div>
</section>

<style>
  .tray {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 6px;
  }
  .tpl {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 6px 2px 4px;
    font-size: 11px;
    min-width: 0;
    cursor: grab;
  }
  .tpl.add {
    cursor: pointer;
    border-style: dashed;
  }
  .name {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  small {
    color: var(--muted);
  }
  .plus {
    width: 36px;
    height: 36px;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 22px;
    color: var(--muted);
  }
  .active {
    border-color: var(--accent);
    background: #3a3420;
  }
</style>
