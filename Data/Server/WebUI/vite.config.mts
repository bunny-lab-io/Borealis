////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  server: {
    open: true,
    host: true,
    strictPort: true,
    // Allow LAN/IP access during dev (so other devices can reach Vite)
    // If you want to restrict, replace `true` with an explicit allowlist.
    allowedHosts: true,
    proxy: {
      // Ensure cookies/headers are forwarded correctly to Flask
      '/api': {
        target: 'http://127.0.0.1:5000',
        changeOrigin: true,
      },
      '/socket.io': { target:'ws://127.0.0.1:5000', ws:true, changeOrigin: true }
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
