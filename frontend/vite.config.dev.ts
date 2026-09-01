import { defineConfig, mergeConfig } from "vite";
import baseConfig from "./vite.config";

export default mergeConfig(baseConfig, defineConfig({
  server: {
    port: 5173,
    allowedHosts: true,
    proxy: { "/api": "http://localhost:8080" },
  },
}));
