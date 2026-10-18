// Replaces the Wails IPC when the frontend is served by the SpotiFLAC web
// server (`vite build --mode web`). The generated wailsjs bindings call
// window.go.main.App.* and window.runtime.*, so providing those objects is
// enough; no component has to know which transport is used.
import { showOpenURLPrompt, showServerDialog, type ServerDialogRequest } from "./serverDialog";

type EventCallback = (...data: unknown[]) => void;
interface Listener {
    callback: EventCallback;
    remaining: number;
}

const REQUEST_HEADER = "X-SpotiFLAC-Request";
const DIALOG_EVENT = "spotiflac:web-dialog";
const OPEN_URL_EVENT = "spotiflac:web-open-url";

const listeners = new Map<string, Listener[]>();

async function callBackend(method: string, args: unknown[]): Promise<unknown> {
    const response = await fetch(`/api/call/${encodeURIComponent(method)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", [REQUEST_HEADER]: "1" },
        body: JSON.stringify(args.map((arg) => (arg === undefined ? null : arg))),
    });
    const text = await response.text();
    const payload = text ? JSON.parse(text) : null;
    if (!response.ok) {
        throw payload && typeof payload.error === "string" ? payload.error : `HTTP ${response.status}`;
    }
    return payload;
}

function dispatch(name: string, data: unknown[]) {
    const current = listeners.get(name);
    if (!current) {
        return;
    }
    for (const listener of [...current]) {
        try {
            listener.callback(...data);
        }
        catch (err) {
            console.error(`Event listener for ${name} failed:`, err);
        }
        if (listener.remaining > 0 && --listener.remaining === 0) {
            removeListener(name, listener);
        }
    }
}

function removeListener(name: string, listener: Listener) {
    const current = listeners.get(name);
    if (!current) {
        return;
    }
    const next = current.filter((item) => item !== listener);
    if (next.length > 0) {
        listeners.set(name, next);
    }
    else {
        listeners.delete(name);
    }
}

function eventsOnMultiple(name: string, callback: EventCallback, maxCallbacks: number) {
    const listener: Listener = { callback, remaining: maxCallbacks };
    listeners.set(name, [...(listeners.get(name) ?? []), listener]);
    return () => removeListener(name, listener);
}

function connectEvents() {
    const source = new EventSource("/api/events");
    source.onmessage = (message) => {
        try {
            const event = JSON.parse(message.data) as { name: string; data: unknown[] };
            dispatch(event.name, event.data ?? []);
        }
        catch (err) {
            console.error("Invalid event from server:", err);
        }
    };
    // EventSource reconnects on its own after network errors.
}

async function answerDialog(id: string, paths: string[] | null) {
    await fetch(`/api/dialog/${encodeURIComponent(id)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", [REQUEST_HEADER]: "1" },
        body: JSON.stringify({ cancelled: paths === null, paths: paths ?? [] }),
    });
}

const noop = () => { };

const runtime: Record<string, unknown> = {
    EventsOnMultiple: eventsOnMultiple,
    EventsOn: (name: string, callback: EventCallback) => eventsOnMultiple(name, callback, -1),
    EventsOnce: (name: string, callback: EventCallback) => eventsOnMultiple(name, callback, 1),
    EventsOff: (name: string, ...additional: string[]) => {
        for (const item of [name, ...additional]) {
            listeners.delete(item);
        }
    },
    EventsOffAll: () => listeners.clear(),
    EventsEmit: (name: string, ...data: unknown[]) => dispatch(name, data),
    BrowserOpenURL: (url: string) => {
        window.open(url, "_blank", "noopener,noreferrer");
    },
    ClipboardGetText: async () => {
        try {
            return await navigator.clipboard.readText();
        }
        catch {
            return "";
        }
    },
    ClipboardSetText: async (text: string) => {
        try {
            await navigator.clipboard.writeText(text);
            return true;
        }
        catch {
            return false;
        }
    },
    // Files dropped into the browser live on the client, not on the server,
    // so drag & drop of local paths is not available in the web interface.
    OnFileDrop: noop,
    OnFileDropOff: noop,
    Environment: async () => ({ buildType: "production", platform: "web", arch: "" }),
};

const runtimeProxy = new Proxy(runtime, {
    get: (target, prop: string) => (prop in target ? target[prop] : noop),
});

const appProxy = new Proxy({}, {
    get: (_target, prop: string) => (...args: unknown[]) => callBackend(prop, args),
});

const w = window as unknown as Record<string, unknown>;
w.runtime = runtimeProxy;
w.go = { main: { App: appProxy } };

eventsOnMultiple(DIALOG_EVENT, (request) => {
    void showServerDialog(request as ServerDialogRequest).then((paths) => answerDialog((request as ServerDialogRequest).id, paths));
}, -1);

eventsOnMultiple(OPEN_URL_EVENT, (target) => {
    showOpenURLPrompt(rewriteLoopbackCallback(String(target)));
}, -1);

// The community verification page redirects to a loopback listener of the
// backend. Seen from the browser that is the wrong machine, so route the
// callback through the web server, which forwards it locally.
function rewriteLoopbackCallback(target: string): string {
    try {
        const url = new URL(target);
        const cb = url.searchParams.get("cb");
        if (!cb) {
            return target;
        }
        const callback = new URL(cb);
        if (callback.hostname !== "127.0.0.1" || callback.pathname !== "/session-grant") {
            return target;
        }
        url.searchParams.set("cb", `${window.location.origin}/api/community-callback/${callback.port}/session-grant${callback.search}`);
        return url.toString();
    }
    catch {
        return target;
    }
}

connectEvents();
