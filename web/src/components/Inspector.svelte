<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { hpColor } from "../lib/hp";
  import type { Command } from "../lib/types";

  let { send }: { send: (c: Command) => Promise<unknown> } = $props();

  const item = $derived.by(() => {
    app.rev;
    const s = app.scene, sel = app.selection;
    if (!s || !sel) return null;
    if (sel.kind === "object" && s.objects[sel.id]) return { kind: "object" as const, o: { ...s.objects[sel.id] } };
    if (sel.kind === "token" && s.tokens[sel.id]) return { kind: "token" as const, t: { ...s.tokens[sel.id] } };
    if (sel.kind === "wall" && s.walls[sel.id]) return { kind: "wall" as const, w: { ...s.walls[sel.id] } };
    return null;
  });

  const assetName = (id: string) => app.catalog?.assets.find((a) => a.id === id)?.name ?? id;

  function remove() {
    const sel = app.selection;
    if (!sel) return;
    const type = { object: "remove_objects", token: "remove_tokens", wall: "remove_walls" }[sel.kind];
    send({ type, ids: [sel.id] });
    app.selection = null;
  }

  let amount = $state(1);

  function adjustHp(id: string, sign: number) {
    const n = Math.round(Math.abs(amount));
    if (n) send({ type: "update_token", id, hpDelta: sign * n });
  }

  const num = (e: Event) => Number((e.currentTarget as HTMLInputElement).value);
  const str = (e: Event) => (e.currentTarget as HTMLInputElement).value;
</script>

{#if item}
  <section>
    {#if item.kind === "object"}
      {@const o = item.o}
      <h3>{assetName(o.assetId)} <small>{o.id}</small></h3>
      <label class="row">Rotation <input type="range" min="0" max="345" step="15" value={o.rotation} onchange={(e) => send({ type: "update_object", id: o.id, rotation: num(e) })} /> {o.rotation}°</label>
      <label class="row">Scale <input type="number" min="0.1" max="20" step="0.1" value={o.scale} onchange={(e) => send({ type: "update_object", id: o.id, scale: num(e) })} /></label>
      <label class="row">Layer
        <select value={o.layer} onchange={(e) => send({ type: "update_object", id: o.id, layer: str(e) })}>
          <option value="floor">Floor (under tokens)</option>
          <option value="overhead">Overhead (over tokens)</option>
        </select>
      </label>
      <label class="row">Order <input type="number" step="1" value={o.z} onchange={(e) => send({ type: "update_object", id: o.id, z: num(e) })} /></label>
    {:else if item.kind === "token"}
      {@const t = item.t}
      <h3>{t.name} <small>{t.id}</small></h3>
      {#if t.maxHp > 0}
        <div class="hp" title="{t.hp} / {t.maxHp} HP">
          <div class="fill" style:width="{(100 * t.hp) / t.maxHp}%" style:background={hpColor(t.hp / t.maxHp)}></div>
          <span>{t.hp} / {t.maxHp} HP</span>
        </div>
        <div class="row">
          <input type="number" min="1" bind:value={amount} onkeydown={(e) => e.key === "Enter" && adjustHp(t.id, e.shiftKey ? 1 : -1)} title="Enter: damage, Shift+Enter: heal" />
          <button onclick={() => adjustHp(t.id, -1)}>Damage</button>
          <button onclick={() => adjustHp(t.id, 1)}>Heal</button>
        </div>
      {/if}
      <button onclick={() => (app.editing = { kind: "token", id: t.id })}>Edit…</button>
    {:else}
      {@const w = item.w}
      <h3>Wall <small>{w.id}</small></h3>
      <p class="hint">{app.catalog?.wallStyles.find((s) => s.id === w.style)?.name ?? w.style}, {w.points.length} points</p>
    {/if}
    <button class="danger" onclick={remove}>Delete</button>
  </section>
{/if}

<style>
  .hp {
    position: relative;
    height: 20px;
    border-radius: 4px;
    background: #111;
    border: 1px solid var(--border);
    margin-bottom: 6px;
    overflow: hidden;
  }
  .fill {
    position: absolute;
    inset: 0 auto 0 0;
  }
  .hp span {
    position: relative;
    display: block;
    text-align: center;
    font-size: 12px;
    line-height: 18px;
    text-shadow: 0 1px 2px #000;
  }
</style>
