// Runtime configuration, loaded by index.html before the app starts.
//
// This default is for local development (`npm run dev`): the API is on the same origin
// (proxied by Vite) and there is no sign-in.
//
// When the Go backend serves the built UI (the Docker image), it answers /config.js
// itself, turning on Keycloak sign-in when OIDC_ISSUER is configured:
//   window.configs = { apiUrl: "", managedAuth: true, authMode: "oidc" };
//
// On Choreo this file is REPLACED by a file mount on the web app component, e.g.
//   window.configs = {
//     apiUrl: "/choreo-apis/<path from the connection's service URL>",
//     managedAuth: true,
//   };
// Everything here is visible in the browser - never put secrets in it.
window.configs = {
  apiUrl: "",
  managedAuth: false,
};
