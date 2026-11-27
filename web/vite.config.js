import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const allowedHosts = ['.' + (process.env.BASE44_SANDBOX_HOST_DOMAIN || 'localhost')];
if (process.env.BASE44_PUBLIC_HOST_SUFFIX) {
  allowedHosts.push('3000-' + process.env.BASE44_PUBLIC_HOST_SUFFIX);
}

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 3000,
    strictPort: true,
    allowedHosts: ['.localhost', ...allowedHosts],
    proxy: {
      '/api': { target: process.env.API_PROXY_TARGET || 'http://api:8080', changeOrigin: false },
    },
  },
});
