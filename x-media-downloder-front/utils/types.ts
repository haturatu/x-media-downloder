export interface Tag {
  tag: string;
  confidence?: number; // Optional, as it might not always be returned
  count?: number; // For tags API
}

export interface Image {
  path: string;
  tags?: Tag[];
  mtime?: number; // Only used internally by backend for sorting
}

export interface PagedResponse<T> {
  items: T[];
  total_items: number;
  per_page: number;
  current_page: number;
  total_pages: number;
}

export interface User {
  username: string;
  tweet_count: number;
}

export interface Tweet {
  tweet_id: string;
  images: Image[];
}