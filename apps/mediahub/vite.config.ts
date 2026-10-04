/// <reference types="vitest" />
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

const dirname = path.dirname(fileURLToPath(import.meta.url));
const BASE = '/mediahub/';
const TUNNEL_HOSTS = ['.ngrok-free.app', '.ngrok-free.dev', '.ngrok.app', '.ngrok.io', '.ngrok.dev'];

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, dirname, '');
  const publicHost = env.VITE_DEV_PUBLIC_HOST?.trim();
  const proxy = {
    '/api': { target: env.GATEWAY_URL || 'http://127.0.0.1:8080', changeOrigin: false, xfwd: true },
  };
  return {
    base: BASE,
    plugins: [react()],
    resolve: { alias: { '@': path.resolve(dirname, 'src') } },
    server: {
      host: true,
      port: 5175,
      strictPort: true,
      allowedHosts: TUNNEL_HOSTS,
      hmr: publicHost ? { host: publicHost, protocol: 'wss' as const, clientPort: 443 } : undefined,
      proxy,
    },
    preview: { host: true, port: 4175, proxy, allowedHosts: TUNNEL_HOSTS },
    build: { target: ['es2020', 'safari13'], sourcemap: true },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./test/setup.ts'],
      include: ['src/**/*.test.{ts,tsx}'],
      restoreMocks: true,
      env: { VITE_API_URL: 'http://api.test' },
    },
  };
});
