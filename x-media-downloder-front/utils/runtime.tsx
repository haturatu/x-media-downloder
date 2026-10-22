import { createContext, toChildArray, type ComponentChildren, type VNode } from "preact";
import { useContext, useEffect } from "preact/hooks";

export const IS_BROWSER = typeof window !== "undefined";
export const HeadContext = createContext<ComponentChildren[] | null>(null);

// Collect metadata per SSR request, and update it after browser hydration.
export function Head({ children }: { children: ComponentChildren }) {
  const head = useContext(HeadContext);
  if (head) head.push(children);
  useEffect(() => {
    const title = toChildArray(children).find(
      (child) => typeof child === "object" && child.type === "title",
    ) as VNode<{ children: ComponentChildren }> | undefined;
    if (title) document.title = toChildArray(title.props.children).join("");
  }, [children]);
  return null;
}

export const SearchContext = createContext("");

export function useQueryParam() {
  const search = useContext(SearchContext);
  return (key: string, fallback: string): string =>
    new URLSearchParams(IS_BROWSER ? window.location.search : search).get(key) ?? fallback;
}
