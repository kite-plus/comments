import { defineConfig } from "vite";

// embed.js: one small classic script for the host page, no module loading
// and no dependencies (widget.md section 10, at most 4 KB gzip).
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: false,
    target: "es2018",
    lib: { entry: "src/embed.ts", formats: ["iife"], name: "KiteComments", fileName: () => "embed.js" },
    minify: "esbuild",
  },
});
