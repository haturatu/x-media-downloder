import { defineConfig } from "vite";

export default defineConfig({
  publicDir: "static",
  esbuild: { jsx: "automatic", jsxImportSource: "preact" },
  build: { outDir: "dist/client" },
});
