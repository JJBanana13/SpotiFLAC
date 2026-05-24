// Minimal fetch wrapper. Sends cookies, returns parsed JSON, throws ApiError on non-2xx.

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: "include",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  };
  const resp = await fetch(path, init);
  if (!resp.ok) {
    let msg = resp.statusText;
    try {
      const data = await resp.json();
      if (data && typeof data === "object" && "message" in data) {
        msg = String((data as { message?: unknown }).message ?? msg);
      }
    } catch {
      // ignore parse error
    }
    throw new ApiError(msg, resp.status);
  }
  if (resp.status === 204) return undefined as T;
  const ctype = resp.headers.get("content-type") || "";
  if (ctype.includes("application/json")) {
    return (await resp.json()) as T;
  }
  return (await resp.text()) as unknown as T;
}

export const api = {
  get: <T>(path: string) => call<T>("GET", path),
  post: <T>(path: string, body?: unknown) => call<T>("POST", path, body),
  delete: <T>(path: string) => call<T>("DELETE", path),
};

/** Trigger a browser download via POST. Resolves once the browser starts saving the file. */
export async function postDownload(path: string, body: unknown): Promise<void> {
  const resp = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!resp.ok) {
    let msg = resp.statusText;
    try {
      const data = await resp.json();
      if (data && typeof data === "object" && "message" in data) {
        msg = String((data as { message?: unknown }).message ?? msg);
      }
    } catch {
      // ignore
    }
    throw new ApiError(msg, resp.status);
  }
  const disposition = resp.headers.get("content-disposition") || "";
  const match = /filename="([^"]+)"/i.exec(disposition);
  const filename = match ? match[1] : "download";
  const blob = await resp.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
