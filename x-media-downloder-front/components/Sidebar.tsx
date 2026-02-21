import { useEffect, useState } from "preact/hooks";
import type { PagedResponse, Tag, User } from "../utils/types.ts";
import { getApiBaseUrl } from "../utils/api.ts";

interface SidebarProps {
  onNavigate?: () => void;
}

export default function Sidebar({ onNavigate }: SidebarProps) {
  const [activeTab, setActiveTab] = useState<"users" | "tags">("users");
  const [userSearchQuery, setUserSearchQuery] = useState<string>("");
  const [tagSearchQuery, setTagSearchQuery] = useState<string>("");
  const [users, setUsers] = useState<User[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [downloadUrls, setDownloadUrls] = useState<string>("");
  const [downloading, setDownloading] = useState<boolean>(false);
  const [statusMessage, setStatusMessage] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  useEffect(() => {
    // Initial load for users and tags
    fetchUsers();
    fetchTags();
  }, []);

  const fetchUsers = async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/api/users?q=${userSearchQuery}`);
      const data: PagedResponse<User> = await res.json();
      setUsers(data.items);
    } catch (error) {
      console.error("Error fetching users:", error);
    }
  };

  const fetchTags = async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/api/tags?q=${tagSearchQuery}`);
      const data: PagedResponse<Tag> = await res.json();
      setTags(data.items);
    } catch (error) {
      console.error("Error fetching tags:", error);
    }
  };

  useEffect(() => {
    const handler = setTimeout(() => {
      fetchUsers();
    }, 300); // Debounce user search
    return () => clearTimeout(handler);
  }, [userSearchQuery]);

  useEffect(() => {
    const handler = setTimeout(() => {
      fetchTags();
    }, 300); // Debounce tag search
    return () => clearTimeout(handler);
  }, [tagSearchQuery]);

  const handleDownload = async () => {
    const urls = downloadUrls.trim().split(/[\s,]+/).filter(Boolean);
    if (!urls.length) {
      alert("Please enter at least one URL.");
      return;
    }

    setDownloading(true);
    setStatusMessage("Sending to background queue...");

    try {
      const res = await fetch(`${API_BASE_URL}/api/download`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ urls }),
      });
      const data = await res.json();
      if (data.success) {
        setStatusMessage(data.message);
        setDownloadUrls("");
        setTimeout(() => {
          fetchUsers(); // Refresh user list after download
          setStatusMessage(null);
        }, 3000);
      } else {
        setStatusMessage(`Error: ${data.message}`);
      }
    } catch (error) {
      console.error("Download error:", error);
      setStatusMessage(`Error: ${error.message}`);
    } finally {
      setDownloading(false);
    }
  };

  return (
    <aside class="sidebar">
      <div class="download-section">
        <h2>Downloader</h2>
        <p class="section-caption">Tweet/X URLを貼り付けて一括取得</p>
        <textarea
          id="tweetUrls"
          placeholder="Enter Tweet URLs..."
          rows={3}
          value={downloadUrls}
          onInput={(e) => setDownloadUrls(e.currentTarget.value)}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
              e.preventDefault();
              if (!downloading) {
                void handleDownload();
              }
            }
          }}
          disabled={downloading}
        >
        </textarea>
        <p class="section-caption">Shortcut: Ctrl+Enter (Cmd+Enter on Mac)</p>
        <button
          type="button"
          id="downloadBtn"
          onClick={handleDownload}
          disabled={downloading}
          class="btn btn-primary"
        >
          {downloading ? "Queuing..." : "Download Media"}
        </button>
        <a href="/download-status" class="download-status-link">
          View Queue Status
        </a>
        {statusMessage && <p class="muted-message">{statusMessage}</p>}
      </div>

      <nav class="sidebar-nav">
        <div class="sidebar-tabs">
          <button
            type="button"
            class={`sidebar-tab-btn ${activeTab === "users" ? "active" : ""}`}
            onClick={() => setActiveTab("users")}
          >
            Users
          </button>
          <button
            type="button"
            class={`sidebar-tab-btn ${activeTab === "tags" ? "active" : ""}`}
            onClick={() => setActiveTab("tags")}
          >
            Tags
          </button>
        </div>

        <div class="sidebar-tab-content">
          <div class={`tab-pane ${activeTab === "users" ? "active" : ""}`}>
            <input
              type="search"
              class="search-box"
              placeholder="Search users..."
              value={userSearchQuery}
              onInput={(e) => setUserSearchQuery(e.currentTarget.value)}
            />
            <ul class="nav-list">
              {users.map((user) => (
                <li key={user.username}>
                  <div class="user-item-link">
                    <a href={`/users/${user.username}`} onClick={onNavigate}>
                      {user.username}
                    </a>
                    <span class="item-count">{user.tweet_count}</span>
                  </div>
                </li>
              ))}
            </ul>
          </div>

          <div class={`tab-pane ${activeTab === "tags" ? "active" : ""}`}>
            <input
              type="search"
              class="search-box"
              placeholder="Search tags..."
              value={tagSearchQuery}
              onInput={(e) => setTagSearchQuery(e.currentTarget.value)}
            />
            <ul class="tag-list">
              {tags.map((tag) => (
                <li key={tag.tag}>
                  <div class="tag-item">
                    <a href={`/tags/${encodeURIComponent(tag.tag)}`} onClick={onNavigate}>
                      {tag.tag}
                    </a>
                    <span class="item-count">{tag.count}</span>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        </div>
      </nav>
    </aside>
  );
}
