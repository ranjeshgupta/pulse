import { defineConfig } from "vite";

// Builds the web SDK as one self-contained file, dist/sdk/pulse.js, exposing window.Pulse.
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: false, // the app build runs first and owns the rest of dist/
    lib: { entry: "src/sdk/pulse.ts", name: "Pulse", formats: ["iife"], fileName: () => "sdk/pulse.js" },
    minify: true,
    target: "es2019",
  },
});
