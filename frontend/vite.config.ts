import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // Relative asset URLs so the app also works behind a path prefix
  // (e.g. Choreo's gateway serves it under /<project>/<component>/<version>/).
  base: "./",
  server: {
    port: 5173,
    proxy: { "/api": "http://localhost:8080" },
  },
});
