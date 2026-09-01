import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    // Go embeds this directory so the production binary serves the exact
    // frontend build from the same origin as the REST API.
    outDir: "../backend/cmd/roomusic/web",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
