////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/vite.config.ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  server: {
    open: true,
    host: true, // <-- allows LAN access and shows LAN IP
    proxy: {
      '/api': 'http://localhost:5000',
      '/socket.io': {
        target: 'ws://localhost:5000',
        ws: true
      }
    }
  },
  build: {
    outDir: 'build',
    emptyOutDir: true
  },
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
    extensions: ['.js', '.jsx', '.ts', '.tsx']
  }
});
