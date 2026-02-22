export interface TaskStatus {
  state?: string;
  status?: string;
  message?: string;
  current?: number;
  total?: number;
}

export interface DownloadStatus {
  queue_depth?: number;
  summary?: {
    total?: number;
    pending?: number;
    success?: number;
    failure?: number;
  };
  items?: unknown[];
}

export interface LiveStatusPayload {
  type: "status";
  ts: number;
  autotag: TaskStatus;
  retag: TaskStatus;
  download: DownloadStatus;
  locks: {
    autotagBusy: boolean;
    retagBusy: boolean;
  };
}

type Listener = (payload: LiveStatusPayload) => void;

const listeners = new Set<Listener>();
let ws: WebSocket | null = null;
let reconnectTimer: number | null = null;

function wsUrl(): string {
  const protocol = globalThis.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${globalThis.location.host}/api/ws/status`;
}

function broadcast(payload: LiveStatusPayload) {
  for (const listener of listeners) {
    listener(payload);
  }
}

function cleanupReconnectTimer() {
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

function connect() {
  if (typeof globalThis.window === "undefined") return;
  if (ws) return;
  ws = new WebSocket(wsUrl());

  ws.onmessage = (event) => {
    try {
      const payload = JSON.parse(String(event.data)) as LiveStatusPayload;
      if (payload && payload.type === "status") {
        broadcast(payload);
      }
    } catch {
      // Ignore malformed payloads.
    }
  };

  ws.onclose = () => {
    ws = null;
    if (listeners.size === 0) return;
    cleanupReconnectTimer();
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, 1000) as unknown as number;
  };

  ws.onerror = () => {
    // Let onclose handle reconnection.
  };
}

export function subscribeStatus(listener: Listener): () => void {
  listeners.add(listener);
  connect();

  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && ws) {
      ws.close();
      ws = null;
      cleanupReconnectTimer();
    }
  };
}
