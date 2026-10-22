// Browsers call the same-origin proxy; SSR calls the Queue API directly.
export function getApiBaseUrl(): string {
  if (typeof window !== "undefined") return "";
  return process.env.API_BASE_URL || process.env.ASYNQ_API_BASE_URL || "http://localhost:8001";
}
