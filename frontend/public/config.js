// Runtime configuration, loaded by index.html before the app starts.
//
// This default is for local development and the single-container Docker image:
// the API is on the same origin and there is no sign-in.
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
