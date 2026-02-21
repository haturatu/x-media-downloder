export function getMediaRoot(): string {
  return Deno.env.get("MEDIA_ROOT") || "./downloaded_images";
}
