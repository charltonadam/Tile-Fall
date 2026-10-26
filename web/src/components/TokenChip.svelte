<script lang="ts">
  // A token's face as HTML (the map draws the same thing in Pixi, see MapView.syncTokens).
  let { name, color, imageUrl = "", px = 40 }: { name: string; color: string; imageUrl?: string; px?: number } = $props();
</script>

<span class="chip" style:background={color} style:width="{px}px" style:height="{px}px" style:font-size="{px * 0.45}px">
  {#if imageUrl}
    <img src={imageUrl} alt="" draggable="false" />
  {:else}
    {(name[0] ?? "?").toUpperCase()}
  {/if}
</span>

<style>
  .chip {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    border-radius: 50%;
    border: 2px solid #fff;
    color: #fff;
    font-weight: bold;
    overflow: hidden;
    box-shadow: 0 1px 3px #0008;
  }
  img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
</style>
