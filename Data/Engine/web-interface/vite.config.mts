////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import fs from 'fs';

const runtimeCertDir = process.env.BOREALIS_CERT_DIR;

const certCandidates = [
  process.env.BOREALIS_TLS_CERT,
  runtimeCertDir && path.resolve(runtimeCertDir, 'borealis-server-cert.pem'),
  path.resolve(__dirname, '../certs/borealis-server-cert.pem'),
  path.resolve(__dirname, '../../../Server/Borealis/certs/borealis-server-cert.pem'),
] as const;

const keyCandidates = [
  process.env.BOREALIS_TLS_KEY,
  runtimeCertDir && path.resolve(runtimeCertDir, 'borealis-server-key.pem'),
  path.resolve(__dirname, '../certs/borealis-server-key.pem'),
  path.resolve(__dirname, '../../../Server/Borealis/certs/borealis-server-key.pem'),
] as const;

const pickFirst = (candidates: readonly (string | undefined)[]) => {
  for (const candidate of candidates) {
    if (!candidate) continue;
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }
  return undefined;
};

const certPath = pickFirst(certCandidates);
const keyPath = pickFirst(keyCandidates);
const engineTlsDisabled = /^(1|true|yes|on)$/i.test(process.env.BOREALIS_DISABLE_ENGINE_TLS || "");
const engineHttpTarget = `${engineTlsDisabled ? 'http' : 'https'}://127.0.0.1:5000`;
const engineWsTarget = `${engineTlsDisabled ? 'ws' : 'wss'}://127.0.0.1:5000`;

const httpsOptions = certPath && keyPath
  ? {
      cert: fs.readFileSync(certPath),
      key: fs.readFileSync(keyPath),
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
    },
  },
  server: {
    open: true,
    host: true,
    strictPort: true,
    // Allow LAN/IP access during dev (so other devices can reach Vite)
    // If you want to restrict, replace `true` with an explicit allowlist.
    allowedHosts: true,
    https: httpsOptions,
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
  }
});
