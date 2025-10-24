////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import fs from 'fs';

const runtimeCertDir = process.env.BOREALIS_CERT_DIR;

const guessCertificateDirectories = () => {
  const hints = new Set<string>();

  if (runtimeCertDir) {
    hints.add(runtimeCertDir);
  }

  const rootHint = process.env.BOREALIS_ROOT;
  if (rootHint) {
    hints.add(path.resolve(rootHint, 'Certificates', 'Server'));
    hints.add(path.resolve(rootHint, 'Data', 'Certificates', 'Server'));
  }

  const repoRoot = path.resolve(__dirname, '../../..');
  hints.add(path.resolve(repoRoot, 'Certificates', 'Server'));
  hints.add(path.resolve(repoRoot, 'Data', 'Certificates', 'Server'));
  hints.add(path.resolve(repoRoot, 'Server', 'Borealis', 'certs'));

  const cwd = process.cwd();
  hints.add(path.resolve(cwd, 'Certificates', 'Server'));
  hints.add(path.resolve(cwd, 'Data', 'Certificates', 'Server'));

  hints.add(path.resolve(__dirname, '../certs'));

  return Array.from(hints);
};

const expandCandidates = (fileName: string, explicit?: string) => {
  const directories = guessCertificateDirectories();
  const candidates: (string | undefined)[] = directories.map((dir) =>
    path.resolve(dir, fileName)
  );
  if (explicit) {
    candidates.unshift(explicit);
  }
  return candidates;
};

const certCandidates = expandCandidates('borealis-server-cert.pem', process.env.BOREALIS_TLS_CERT);
const keyCandidates = expandCandidates('borealis-server-key.pem', process.env.BOREALIS_TLS_KEY);

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

const httpsOptions = certPath && keyPath
  ? {
      cert: fs.readFileSync(certPath),
      key: fs.readFileSync(keyPath),
    }
  : undefined;

if (!httpsOptions) {
  console.warn(
    '[Borealis] TLS certificate material not found for Vite dev server; falling back to HTTP. '
    + 'Ensure the Engine is running so certificates are provisioned, or set BOREALIS_CERT_DIR explicitly.'
  );
}

export default defineConfig({
  plugins: [react()],
  server: {
    open: true,
    host: true,
    strictPort: true,
    // Allow LAN/IP access during dev (so other devices can reach Vite)
    // If you want to restrict, replace `true` with an explicit allowlist.
    allowedHosts: true,
    https: httpsOptions,
    proxy: {
      // Ensure cookies/headers are forwarded correctly to Flask over TLS
      '/api': {
        target: 'https://127.0.0.1:5000',
        changeOrigin: true,
        secure: false,
      },
      '/socket.io': {
        target: 'wss://127.0.0.1:5000',
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
