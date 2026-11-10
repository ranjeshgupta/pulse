import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the API runs in Docker on :7080; proxying keeps the app same-origin, so the
// session cookie and CSRF header behave exactly as in production (where Go serves the built app).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/v1": "http://localhost:7080" },
  },
  build: { outDir: "dist", sourcemap: false, chunkSizeWarningLimit: 1500 },
  // The tested modules (rule tree, journey graph) are pure logic: no DOM needed.
  test: { environment: "node" },
});
