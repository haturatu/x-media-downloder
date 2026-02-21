interface CacheEntry<T> {
  expiresAt: number;
  value: T;
}

const responseCache = new Map<string, CacheEntry<unknown>>();

export const DEFAULT_API_CACHE_TTL_MS = 10_000;

export function buildCacheKey(pathname: string, search: string): string {
  return `${pathname}?${search}`;
}

export function getCachedValue<T>(key: string): T | null {
  const entry = responseCache.get(key);
  if (!entry) {
    return null;
  }

  if (entry.expiresAt <= Date.now()) {
    responseCache.delete(key);
    return null;
  }

  return entry.value as T;
}

export function setCachedValue<T>(
  key: string,
  value: T,
  ttlMs = DEFAULT_API_CACHE_TTL_MS,
): void {
  responseCache.set(key, {
    expiresAt: Date.now() + ttlMs,
    value,
  });
}

export function invalidateCacheByPrefix(prefix: string): void {
  for (const key of responseCache.keys()) {
    if (key.startsWith(prefix)) {
      responseCache.delete(key);
    }
  }
}
