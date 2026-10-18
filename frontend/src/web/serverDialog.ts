// Minimal file browser for the web interface. Native dialogs cannot be shown
// for the server's file system, so the backend asks the browser to render
// this instead and waits for the selected server paths.
export interface ServerDialogRequest {
    id: string;
    kind: "directory" | "file" | "files" | "save";
    title: string;
    defaultDirectory: string;
    defaultFilename: string;
    filters: { DisplayName: string; Pattern: string }[] | null;
}

interface DirListing {
    path: string;
    parent: string;
    sep: string;
    roots: string[];
    entries: { name: string; isDir: boolean }[];
}

const STYLE_ID = "spotiflac-server-dialog-style";
const STYLE = `
.sfd-backdrop{position:fixed;inset:0;z-index:2147483000;display:grid;place-items:center;background:rgba(0,0,0,.55);font:13px/1.4 inherit}
.sfd{width:min(640px,calc(100vw - 32px));max-height:min(560px,calc(100vh - 32px));display:flex;flex-direction:column;gap:10px;padding:16px;border-radius:12px;background:var(--background,#111);color:var(--foreground,#eee);border:1px solid var(--border,#333);box-shadow:0 20px 60px rgba(0,0,0,.4)}
.sfd h2{margin:0;font-size:15px;font-weight:600}
.sfd-row{display:flex;gap:6px;align-items:center}
.sfd input,.sfd select{flex:1;min-width:0;height:32px;padding:0 10px;border-radius:8px;border:1px solid var(--border,#333);background:transparent;color:inherit;font:inherit}
.sfd select option{background:var(--background,#111)}
.sfd button{height:32px;padding:0 12px;border-radius:8px;border:1px solid var(--border,#333);background:transparent;color:inherit;font:inherit;cursor:pointer}
.sfd button:hover{background:var(--muted,#222)}
.sfd button.sfd-primary{background:var(--primary,#eee);color:var(--primary-foreground,#111);border-color:transparent}
.sfd-list{flex:1;min-height:160px;overflow:auto;border:1px solid var(--border,#333);border-radius:8px;padding:4px}
.sfd-item{display:flex;gap:8px;align-items:center;padding:5px 8px;border-radius:6px;cursor:pointer;user-select:none;word-break:break-all}
.sfd-item:hover{background:var(--muted,#222)}
.sfd-item.sfd-selected{background:var(--accent,#333);outline:1px solid var(--ring,#666)}
.sfd-empty,.sfd-error{padding:10px;color:var(--muted-foreground,#888)}
.sfd-error{color:#e5484d}
.sfd-actions{display:flex;justify-content:flex-end;gap:8px}
`;

function ensureStyle() {
    if (document.getElementById(STYLE_ID)) {
        return;
    }
    const style = document.createElement("style");
    style.id = STYLE_ID;
    style.textContent = STYLE;
    document.head.appendChild(style);
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, props: Record<string, unknown> = {}, children: (Node | string)[] = []): HTMLElementTagNameMap[K] {
    const node = document.createElement(tag);
    Object.assign(node, props);
    node.append(...children);
    return node;
}

function joinPath(dir: string, name: string, sep: string) {
    return dir.endsWith(sep) ? dir + name : dir + sep + name;
}

function patternMatcher(pattern: string | undefined) {
    const parts = (pattern ?? "*.*").split(";").map((p) => p.trim().toLowerCase()).filter(Boolean);
    if (parts.length === 0 || parts.some((p) => p === "*" || p === "*.*")) {
        return () => true;
    }
    const extensions = parts.map((p) => p.replace(/^\*/, ""));
    return (name: string) => extensions.some((ext) => name.toLowerCase().endsWith(ext));
}

async function listDir(path: string): Promise<DirListing> {
    const response = await fetch(`/api/fs?path=${encodeURIComponent(path)}`);
    const payload = await response.json();
    if (!response.ok) {
        throw new Error(payload?.error ?? `HTTP ${response.status}`);
    }
    return payload as DirListing;
}

export function showServerDialog(request: ServerDialogRequest): Promise<string[] | null> {
    ensureStyle();
    return new Promise((resolve) => {
        const filters = request.filters ?? [];
        const showFiles = request.kind !== "directory";
        const multi = request.kind === "files";
        let listing: DirListing | null = null;
        let matches = patternMatcher(filters[0]?.Pattern);
        const selected = new Set<string>();

        const pathInput = el("input", { value: request.defaultDirectory, spellcheck: false });
        const upButton = el("button", { type: "button", textContent: "↑", title: "Parent folder" });
        const list = el("div", { className: "sfd-list" });
        const filenameInput = el("input", { value: request.defaultFilename, placeholder: "File name", spellcheck: false });
        const filterSelect = el("select");
        for (const [index, filter] of filters.entries()) {
            filterSelect.append(el("option", { value: String(index), textContent: filter.DisplayName }));
        }
        const cancelButton = el("button", { type: "button", textContent: "Cancel" });
        const okButton = el("button", {
            type: "button",
            className: "sfd-primary",
            textContent: request.kind === "directory" ? "Select folder" : request.kind === "save" ? "Save" : "Select",
        });

        const dialog = el("div", { className: "sfd" }, [
            el("h2", { textContent: request.title || (request.kind === "directory" ? "Select folder" : "Select file") }),
            el("div", { className: "sfd-row" }, [upButton, pathInput]),
            list,
            ...(request.kind === "save" ? [el("div", { className: "sfd-row" }, [filenameInput])] : []),
            ...(showFiles && filters.length > 0 ? [el("div", { className: "sfd-row" }, [filterSelect])] : []),
            el("div", { className: "sfd-actions" }, [cancelButton, okButton]),
        ]);
        const backdrop = el("div", { className: "sfd-backdrop" }, [dialog]);

        const finish = (paths: string[] | null) => {
            backdrop.remove();
            document.removeEventListener("keydown", onKey, true);
            resolve(paths);
        };
        const confirm = () => {
            const dir = listing?.path ?? pathInput.value;
            const sep = listing?.sep ?? "/";
            if (request.kind === "directory") {
                finish([dir]);
            }
            else if (request.kind === "save") {
                const name = filenameInput.value.trim();
                if (name) {
                    finish([joinPath(dir, name, sep)]);
                }
            }
            else if (selected.size > 0) {
                finish([...selected]);
            }
        };
        const onKey = (event: KeyboardEvent) => {
            if (event.key === "Escape") {
                event.stopPropagation();
                finish(null);
            }
        };

        const render = () => {
            list.replaceChildren();
            if (!listing) {
                return;
            }
            const current = listing;
            const entries = current.entries.filter((entry) => entry.isDir || (showFiles && matches(entry.name)));
            if (entries.length === 0) {
                list.append(el("div", { className: "sfd-empty", textContent: "Empty folder" }));
            }
            for (const entry of entries) {
                const fullPath = joinPath(current.path, entry.name, current.sep);
                const item = el("div", {
                    className: "sfd-item" + (selected.has(fullPath) ? " sfd-selected" : ""),
                    textContent: `${entry.isDir ? "📁" : "🎵"} ${entry.name}`,
                    title: fullPath,
                });
                item.addEventListener("click", () => {
                    if (entry.isDir) {
                        void navigate(fullPath);
                        return;
                    }
                    if (request.kind === "save") {
                        filenameInput.value = entry.name;
                        return;
                    }
                    if (!multi) {
                        selected.clear();
                    }
                    if (selected.has(fullPath)) {
                        selected.delete(fullPath);
                    }
                    else {
                        selected.add(fullPath);
                    }
                    render();
                });
                if (!entry.isDir && !multi && request.kind !== "save") {
                    item.addEventListener("dblclick", () => finish([fullPath]));
                }
                list.append(item);
            }
        };

        const navigate = async (path: string) => {
            try {
                listing = await listDir(path);
                pathInput.value = listing.path;
                if (!multi) {
                    selected.clear();
                }
                render();
            }
            catch (err) {
                list.replaceChildren(el("div", { className: "sfd-error", textContent: String(err instanceof Error ? err.message : err) }));
            }
        };

        upButton.addEventListener("click", () => {
            if (listing?.parent) {
                void navigate(listing.parent);
            }
        });
        pathInput.addEventListener("keydown", (event) => {
            if (event.key === "Enter") {
                void navigate(pathInput.value);
            }
        });
        filenameInput.addEventListener("keydown", (event) => {
            if (event.key === "Enter") {
                confirm();
            }
        });
        filterSelect.addEventListener("change", () => {
            matches = patternMatcher(filters[Number(filterSelect.value)]?.Pattern);
            render();
        });
        cancelButton.addEventListener("click", () => finish(null));
        okButton.addEventListener("click", confirm);
        document.addEventListener("keydown", onKey, true);

        document.body.appendChild(backdrop);
        void navigate(request.defaultDirectory);
    });
}

// Popup blockers stop window.open() outside of a click, so links opened by the
// backend are shown as a button the user clicks.
export function showOpenURLPrompt(url: string) {
    ensureStyle();
    const link = el("a", { href: url, target: "_blank", rel: "noopener noreferrer", textContent: "Open link" });
    link.className = "sfd-primary";
    Object.assign(link.style, { display: "inline-flex", alignItems: "center", height: "32px", padding: "0 12px", borderRadius: "8px", textDecoration: "none", background: "var(--primary,#eee)", color: "var(--primary-foreground,#111)" });
    const closeButton = el("button", { type: "button", textContent: "Close" });
    const backdrop = el("div", { className: "sfd-backdrop" }, [
        el("div", { className: "sfd" }, [
            el("h2", { textContent: "SpotiFLAC wants to open a page" }),
            el("div", { className: "sfd-empty", textContent: url }),
            el("div", { className: "sfd-actions" }, [closeButton, link]),
        ]),
    ]);
    const close = () => backdrop.remove();
    closeButton.addEventListener("click", close);
    link.addEventListener("click", () => setTimeout(close, 0));
    document.body.appendChild(backdrop);
}
