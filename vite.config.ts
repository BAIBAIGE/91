import { defineConfig, type Plugin, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

const backendTarget = "http://127.0.0.1:9192";
const backendProxy: Record<string, ProxyOptions> = {
  "/api": { target: backendTarget, xfwd: true },
  "/p": { target: backendTarget, xfwd: true },
  "/admin/api": { target: backendTarget, xfwd: true },
};

const hashedAssetCacheControl = "public, max-age=31536000, immutable";

function cacheHashedAssets(
  req: { url?: string },
  res: { setHeader: (name: string, value: string) => void },
  next: () => void
) {
  if (req.url?.startsWith("/assets/")) {
    res.setHeader("Cache-Control", hashedAssetCacheControl);
  }
  next();
}

function hashedAssetCachePlugin(): Plugin {
  return {
    name: "hashed-asset-cache",
    configureServer(server) {
      server.middlewares.use(cacheHashedAssets);
    },
    configurePreviewServer(server) {
      server.middlewares.use(cacheHashedAssets);
    },
  };
}

export default defineConfig({
  plugins: [react(), hashedAssetCachePlugin()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    host: "0.0.0.0",
    port: 9191,
    proxy: backendProxy,
  },
  preview: {
    host: "0.0.0.0",
    port: 9191,
    proxy: backendProxy,
  },
});
