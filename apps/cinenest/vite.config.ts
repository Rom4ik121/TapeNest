/// <reference types="vitest" />
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv, type Plugin } from 'vite';

const dirname = path.dirname(fileURLToPath(import.meta.url));
/** CineNest is served under a path prefix so one origin/tunnel hosts all mini apps (ADR 0007). */
const BASE = '/cinenest/';
const TUNNEL_HOSTS = ['.ngrok-free.app', '.ngrok-free.dev', '.ngrok.app', '.ngrok.io', '.ngrok.dev'];

/**
 * DEV ONLY: serves a generated test HLS stream (dev-assets/mock-hls, see
 * scripts/gen-mock-hls.sh) at /cinenest/__mock-hls/ for the MSW cinema mocks.
 * Not part of the production build (public/ is not used for it).
 */
function mockHls(): Plugin {
  const dir = path.resolve(dirname, 'dev-assets/mock-hls');
  const types: Record<string, string> = { '.m3u8': 'application/vnd.apple.mpegurl', '.ts': 'video/mp2t' };
  return {
    name: 'cinenest-mock-hls',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(`${BASE}__mock-hls/`, (req, res, next) => {
        const name = path.basename((req.url ?? '').split('?')[0] ?? '');
        const file = path.join(dir, name);
        const type = types[path.extname(name)];
        if (!type || !fs.existsSync(file)) return next();
        res.setHeader('Content-Type', type);
        res.setHeader('Cache-Control', 'no-cache');
        fs.createReadStream(file).pipe(res);
      });
    },
  };
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, dirname, '');
  const publicHost = env.VITE_DEV_PUBLIC_HOST?.trim();
  // Opened directly (localhost:5174) the app still reaches the gateway; through the
  // tunnel WavePlayer's Vite (:5173) forwards /cinenest → here and /api → gateway.
  const proxy = {
    '/api': { target: env.GATEWAY_URL || 'http://127.0.0.1:8080', changeOrigin: false, xfwd: true },
  };
  return {
    base: BASE,
    plugins: [react(), mockHls()],
    resolve: { alias: { '@': path.resolve(dirname, 'src') } },
    server: {
      host: true,
      port: 5174,
      strictPort: true,
      allowedHosts: TUNNEL_HOSTS,
      hmr: publicHost ? { host: publicHost, protocol: 'wss', clientPort: 443 } : undefined,
      proxy,
    },
    preview: { host: true, port: 4174, proxy, allowedHosts: TUNNEL_HOSTS },
    build: { target: ['es2020', 'safari13'], sourcemap: true },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./test/setup.ts'],
      include: ['src/**/*.test.{ts,tsx}', 'test/**/*.test.{ts,tsx}'],
      restoreMocks: true,
      env: { VITE_USE_MOCKS: 'false', VITE_API_URL: 'http://api.test' },
    },
  };
});
