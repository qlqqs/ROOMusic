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
  // Vite's server settings are ignored by production builds. Keeping this
  // here also covers panels and scripts that invoke `vite` without a config
  // override during development.
  server: {
    port: 5173,
    allowedHosts: true,
    proxy: { "/api": "http://localhost:8080" },
  },
});
