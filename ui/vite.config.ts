import { defineConfig } from 'vitest/config';
import { env } from 'node:process';

const backendPort = env.DAIMON_DEV_PORT ?? '3000';
if (!/^\d{1,5}$/.test(backendPort) || Number(backendPort) < 1 || Number(backendPort) > 65535) throw new Error('Invalid local development port');

export default defineConfig({
  server: { proxy: { '/api': { target: `http://127.0.0.1:${backendPort}`, changeOrigin: false } } },
  build: { outDir: '../internal/server/ui', emptyOutDir: true, sourcemap: false },
  test: { environment: 'jsdom', restoreMocks: true },
});
