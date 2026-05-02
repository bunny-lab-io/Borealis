////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

const engineHttpTarget = 'http://127.0.0.1:5000';
const engineWsTarget = 'ws://127.0.0.1:5000';
const usePublicEdge = String(process.env.BOREALIS_DEV_UI_PROXY_ENABLED || '').trim() === '1';
const publicHostname = String(process.env.BOREALIS_PUBLIC_HOSTNAME || '').trim();
const publicHttpsPort = Number.parseInt(String(process.env.BOREALIS_PUBLIC_HTTPS_PORT || '443'), 10);
const resolvedHmrClientPort = Number.isFinite(publicHttpsPort) && publicHttpsPort > 0 ? publicHttpsPort : 443;
const devServerHost = usePublicEdge ? '127.0.0.1' : true;
const hmrConfig = usePublicEdge && publicHostname
  ? {
      protocol: 'wss',
      host: publicHostname,
      clientPort: resolvedHmrClientPort,
      path: '/__vite_hmr',
    }
  : undefined;

export default defineConfig({
  plugins: [react()],
  esbuild: {
    target: "es2022",
  },
  optimizeDeps: {
    esbuildOptions: {
      target: "es2022",
      // Firefox surfaces noisy "No sources are declared in this source map" errors
      // for Vite's prebundled dependency maps; keep app debugging intact and
      // suppress vendor-map generation for dev/HMR sessions.
      sourcemap: false,
    },
  },
  server: {
    open: true,
    host: devServerHost,
    strictPort: true,
    allowedHosts: true,
    hmr: hmrConfig,
    proxy: {
      // Ensure cookies/headers are forwarded correctly to the loopback Engine runtime.
      '/api': {
        target: engineHttpTarget,
        changeOrigin: true,
        secure: false,
      },
      '/socket.io': {
        target: engineWsTarget,
        ws: true,
        changeOrigin: true,
        secure: false,
      }
    }
  },
  build: {
    outDir: 'build',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1000,
    target: 'es2022',
    rollupOptions: {
      output: {
        // split each npm package into its own chunk
        manualChunks(id) {
          if (id.includes('node_modules')) {
            return id.toString()
                     .split('node_modules/')[1]
                     .split('/')[0];
          }
        }
      }
    }
  },
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
    extensions: ['.js','.jsx','.ts','.tsx']
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setupTests.js',
    include: ['Unit_Tests/**/*.test.{js,jsx,ts,tsx}'],
  },
});
