import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Прод-сборка отдаётся Go-сервером из-под /app/ → base обязателен.
// В dev /api проксируется на живой сервер стенда (DevAuth — без заголовков).
export default defineConfig({
  base: "/app/",
  plugins: [react()],
  server: {
    proxy: {
      "/api": {
        target: process.env.VITE_API_TARGET || "http://192.168.31.28:8080",
        changeOrigin: true,
      },
    },
  },
});
