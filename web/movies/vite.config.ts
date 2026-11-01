import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// In development the API runs on the Go server (default port 43311).
export default defineConfig({
    plugins: [react(), tailwindcss()],
    resolve: { alias: { "@": new URL("./src", import.meta.url).pathname } },
    server: {
        port: 43100,
        proxy: {
            "/api": { target: "http://127.0.0.1:43311", changeOrigin: false },
        },
    },
    build: {
        outDir: "dist",
        emptyOutDir: true,
        chunkSizeWarningLimit: 1500,
    },
})
