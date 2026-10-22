import { hydrate } from "preact";
import { App, type AppState } from "./app.tsx";

const state: AppState = JSON.parse(document.getElementById("page-data")!.textContent!);
hydrate(<App {...state} />, document.getElementById("app")!);

// Filters/pagination update history without a client router. Reload on Back/Forward
// so the server restores the data and controls for the selected URL.
window.addEventListener("popstate", () => window.location.reload());
