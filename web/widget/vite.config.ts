import { defineConfig } from "vite";

// The iframe app and the login popup's script. Asset names carry hashes and
// are served under /static/widget/ with a long cache; the Go server reads the
// manifest to find them and inlines the app's CSS (widget.md section 4).
export default defineConfig({
  base: "/static/widget/",
  esbuild: { jsx: "automatic", jsxImportSource: "preact" },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    manifest: true,
    target: "es2020",
    // The CSP has no 'unsafe-inline' and requires Trusted Types, so no
    // injected preload helper.
    modulePreload: { polyfill: false },
    cssCodeSplit: true,
    rollupOptions: {
      input: { app: "src/app/main.tsx", popup: "src/popup/popup.ts" },
      output: { entryFileNames: "assets/[name]-[hash].js", assetFileNames: "assets/[name]-[hash][extname]" },
    },
  },
});
