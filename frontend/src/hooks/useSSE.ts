import { useEffect, useRef, useState } from "react";

export interface QueueEvent {
  kind: "added" | "started" | "progress" | "completed" | "failed" | "skipped";
  item_id: string;
  track?: string;
  artist?: string;
  album?: string;
  status?: string;
  progress_mb?: number;
  speed_mbps?: number;
  size_mb?: number;
  error?: string;
}

/**
 * Subscribes to a Server-Sent Events endpoint and invokes `onEvent` for every
 * message. Reconnects automatically on transient errors (handled by the
 * browser's EventSource). The connection is opened only while `enabled` is true.
 */
export function useSSE(
  url: string,
  enabled: boolean,
  onEvent: (ev: QueueEvent) => void
) {
  const handlerRef = useRef(onEvent);
  handlerRef.current = onEvent;

  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!enabled) return;

    const es = new EventSource(url, { withCredentials: true });

    const dispatch = (e: MessageEvent) => {
      try {
        const data: QueueEvent = JSON.parse(e.data);
        handlerRef.current(data);
      } catch {
        // ignore malformed payload
      }
    };

    for (const kind of [
      "added",
      "started",
      "progress",
      "completed",
      "failed",
      "skipped",
    ] as const) {
      es.addEventListener(kind, dispatch as EventListener);
    }

    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);

    return () => {
      es.close();
      setConnected(false);
    };
  }, [url, enabled]);

  return { connected };
}
