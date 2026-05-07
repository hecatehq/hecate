import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/healthz": "http://127.0.0.1:8765",
      "/hecate": "http://127.0.0.1:8765",
      "/v1": "http://127.0.0.1:8765",
    },
  },
});
