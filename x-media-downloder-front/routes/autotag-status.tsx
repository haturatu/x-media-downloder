// x-media-downloder-front/routes/autotag-status.tsx

import AutotagStatusPage from "../islands/AutotagStatusPage.tsx";

// This page is fully client-side rendered, so it just needs to render the island.
// No server-side props are needed.
export default function AutotagStatusRoute() {
  return <AutotagStatusPage />;
}
