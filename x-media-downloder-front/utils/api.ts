// utils/api.ts
export function getApiBaseUrl(): string {
  // Deno.env is only available server-side
  if (typeof Deno !== 'undefined' && Deno.env) {
    // On the server, we fetch from our own host.
    return Deno.env.get("API_BASE_URL") || "http://localhost:8000";
  }
  // On the client, use relative paths to call the API on the same domain.
  return ""; 
}
