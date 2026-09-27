import { defineConfig, mergeConfig } from "vite";
import baseConfig from "./vite.config.ts";

// 公网反代开发时只放行 ROOMUSIC_PUBLIC_URL 的主机名；Vite 默认已放行 localhost 和 IP。
const publicUrl = process.env.ROOMUSIC_PUBLIC_URL;
const allowedHosts = publicUrl ? [new URL(publicUrl).hostname] : [];

export default mergeConfig(baseConfig, defineConfig({
  server: {
    port: 5173,
    allowedHosts,
    proxy: { "/api": "http://localhost:8080" },
  },
}));
