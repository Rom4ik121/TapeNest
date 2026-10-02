/// <reference types="vitest" />
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

const dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, dirname, '');
  // Public dev tunnel host (ngrok). Only needed when the dev server is opened
  // through a tunnel (e.g. inside Telegram); localhost always works.
  const publicHost = env.VITE_DEV_PUBLIC_HOST?.trim();
  // DEV: one origin for everything behind the tunnel — /api → api-gateway,
  // /tg → bot-service (Telegram webhook). Prod: nginx does the same routing.
  const proxy = {
    '/api': { target: env.GATEWAY_URL || 'http://127.0.0.1:8080', changeOrigin: false, xfwd: true },
    '/tg': { target: env.BOT_SERVICE_URL || 'http://127.0.0.1:8081', changeOrigin: false, xfwd: true },
    // CineNest mini app lives under /cinenest/ on the same origin (ADR 0007): its own
    // Vite dev server (base /cinenest/) on :5174; ws for its HMR socket.
    '/cinenest': { target: env.CINENEST_URL || 'http://127.0.0.1:5174', changeOrigin: false, ws: true },
    // Presigned MinIO links for downloads > 50 MB (ADR 0008): the signature covers the
    // public Host, so it must reach MinIO unchanged (changeOrigin: false).
    '/media/': { target: env.MINIO_URL || 'http://127.0.0.1:9000', changeOrigin: false },
  };

  return {
    plugins: [react()],
    resolve: { alias: { '@': path.resolve(dirname, 'src') } },
    server: {
      host: true,
      port: 5173,
      strictPort: true,
      allowedHosts: ['.ngrok-free.app', '.ngrok-free.dev', '.ngrok.app', '.ngrok.io', '.ngrok.dev'],
      // Through an HTTPS tunnel the HMR websocket must go via wss on 443.
      hmr: publicHost ? { host: publicHost, protocol: 'wss', clientPort: 443 } : undefined,
      proxy,
    },
    preview: {
      host: true,
      port: 4173,
      proxy,
      allowedHosts: ['.ngrok-free.app', '.ngrok-free.dev', '.ngrok.app', '.ngrok.io', '.ngrok.dev'],
    },
    build: {
      target: ['es2020', 'safari13'],
      sourcemap: true,
    },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./test/setup.ts'],
      include: ['src/**/*.test.{ts,tsx}', 'test/**/*.test.{ts,tsx}'],
      restoreMocks: true,
      // Tests hit an absolute fake origin intercepted by msw/node.
      env: { VITE_USE_MOCKS: 'false', VITE_API_URL: 'http://api.test' },
    },
  };
});
