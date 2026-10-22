import { SearchContext } from "./utils/runtime.tsx";
import Layout from "./islands/Layout.tsx";
import Home from "./routes/index.tsx";
import Users from "./routes/users.tsx";
import UserTweets from "./routes/users/[username].tsx";
import Tags from "./routes/tags.tsx";
import TagImages from "./routes/tags/[tag].tsx";
import DownloadStatus from "./routes/download-status.tsx";
import AutotagStatus from "./routes/autotag-status.tsx";
import NotFound from "./routes/_404.tsx";

// A discriminated union keeps server data and hydrated page props in agreement.
export type PageState =
  | { page: "home"; data: Parameters<typeof Home>[0]["data"] }
  | { page: "users"; data: Parameters<typeof Users>[0]["data"] }
  | { page: "userTweets"; data: Parameters<typeof UserTweets>[0]["data"] }
  | { page: "tags"; data: Parameters<typeof Tags>[0]["data"] }
  | { page: "tagImages"; data: Parameters<typeof TagImages>[0]["data"] }
  | { page: "downloadStatus" | "autotagStatus" | "notFound"; data: null };

export type AppState = PageState & { route: string; search: string };

export function App(state: AppState) {
  let content;
  switch (state.page) {
    case "home": content = <Home data={state.data} />; break;
    case "users": content = <Users data={state.data} />; break;
    case "userTweets": content = <UserTweets data={state.data} />; break;
    case "tags": content = <Tags data={state.data} />; break;
    case "tagImages": content = <TagImages data={state.data} />; break;
    case "downloadStatus": content = <DownloadStatus />; break;
    case "autotagStatus": content = <AutotagStatus />; break;
    case "notFound": content = <NotFound />; break;
  }
  return <SearchContext.Provider value={state.search}><Layout route={state.route}>{content}</Layout></SearchContext.Provider>;
}
