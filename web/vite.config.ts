import { writeFileSync } from "node:fs";
import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// The build goes straight into the Go package that embeds it. go:embed
// needs the folder to exist even before the first build, so keep a marker.
const outDir = path.resolve(__dirname, "../internal/web/dist");
const keepMarker: Plugin = {
  name: "nexus-keep-marker",
  closeBundle() {
    writeFileSync(path.join(outDir, ".gitkeep"), "");
  },
};

const nexus = process.env.NEXUS_URL ?? "http://127.0.0.1:8090";

export default defineConfig({
  base: "/ui/",
  plugins: [react(), tailwindcss(), keepMarker],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  build: { outDir, emptyOutDir: true },
  server: {
    proxy: { "/api": nexus, "/files": nexus, "/dav": nexus },
  },
});
