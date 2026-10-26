import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

const backend = "http://localhost:8080";

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // /assets/ is the Go server's asset library, so bundles go elsewhere
    assetsDir: "static",
  },
  server: {
    proxy: {
      "/ws": { target: backend, ws: true },
      "/api": backend,
      "/assets": backend,
      "/uploads": backend,
    },
  },
});
