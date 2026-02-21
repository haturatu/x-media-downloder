#!/usr/bin/env -S deno run -A --watch=static/,routes/

import dev from "$fresh/dev.ts";
import config from "./fresh.config.ts";
import { initDb } from "./utils/db.ts";

import "$std/dotenv/load.ts";

// Initialize the database before starting the server
if (Deno.args[0] !== "build") {
  initDb();
}

await dev(import.meta.url, "./main.ts", config);
