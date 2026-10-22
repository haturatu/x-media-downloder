import { hydrate } from "preact";
import { App, type AppState } from "./app.tsx";

const state: AppState = JSON.parse(document.getElementById("page-data")!.textContent!);
hydrate(<App {...state} />, document.getElementById("app")!);
