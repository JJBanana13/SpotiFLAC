import { useCallback, useEffect, useMemo, useState } from "react";
import { ApiError } from "./api/client";
import type { AuthInfo, SearchTrackResult } from "./api/spotiflac";
import {
  downloadTrack,
  health,
  invite,
  login,
  logout,
  me,
  search,
} from "./api/spotiflac";
import { useSSE } from "./hooks/useSSE";

type AuthState = "loading" | "guest" | AuthInfo;

export default function App() {
  const [auth, setAuth] = useState<AuthState>("loading");
  const [version, setVersion] = useState<string>("dev");

  useEffect(() => {
    health()
      .then((h) => setVersion(h.version))
      .catch(() => {});
    me()
      .then((info) => setAuth(info))
      .catch(() => setAuth("guest"));
  }, []);

  const handleLogout = useCallback(async () => {
    await logout();
    setAuth("guest");
  }, []);

  if (auth === "loading") {
    return (
      <div className="min-h-screen flex items-center justify-center text-sm text-zinc-500">
        loading…
      </div>
    );
  }
  if (auth === "guest") {
    return <LoginPage onAuth={setAuth} />;
  }
  return <HomePage user={auth} version={version} onLogout={handleLogout} />;
}

function LoginPage({ onAuth }: { onAuth: (info: AuthInfo) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const info = await login(username.trim(), password);
      onAuth(info);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "login failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-zinc-950 text-zinc-100 p-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm space-y-4 bg-zinc-900 border border-zinc-800 rounded-2xl p-6 shadow-xl"
      >
        <header className="space-y-1 text-center">
          <h1 className="text-xl font-semibold">SpotiFLAC</h1>
          <p className="text-xs text-zinc-400">Anmelden, um Tracks zu laden.</p>
        </header>
        <label className="block space-y-1">
          <span className="text-xs text-zinc-400">Benutzername</span>
          <input
            autoFocus
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            className="w-full rounded-md bg-zinc-800 border border-zinc-700 px-3 py-2 text-sm focus:outline-none focus:border-zinc-500"
            autoComplete="username"
            required
          />
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-zinc-400">Passwort</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md bg-zinc-800 border border-zinc-700 px-3 py-2 text-sm focus:outline-none focus:border-zinc-500"
            autoComplete="current-password"
            required
          />
        </label>
        {error && <p className="text-sm text-red-400">{error}</p>}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded-md bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 py-2 text-sm font-medium"
        >
          {busy ? "…" : "Login"}
        </button>
      </form>
    </div>
  );
}

function HomePage({
  user,
  version,
  onLogout,
}: {
  user: AuthInfo;
  version: string;
  onLogout: () => void;
}) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchTrackResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [downloading, setDownloading] = useState<string | null>(null);
  const [downloadMsg, setDownloadMsg] = useState<string | null>(null);
  const [tidalAPI, setTidalAPI] = useState<string>(
    () => localStorage.getItem("tidal_api_url") || ""
  );
  const [service, setService] = useState<"tidal" | "qobuz" | "amazon">("qobuz");

  // Live progress: server reports MB downloaded + MB/s for the user's active download.
  const [progressMB, setProgressMB] = useState<number | null>(null);
  const [speedMBps, setSpeedMBps] = useState<number | null>(null);

  useEffect(() => {
    localStorage.setItem("tidal_api_url", tidalAPI);
  }, [tidalAPI]);

  useSSE("/api/events/queue", true, (ev) => {
    if (ev.kind === "progress") {
      if (typeof ev.progress_mb === "number") setProgressMB(ev.progress_mb);
      if (typeof ev.speed_mbps === "number") setSpeedMBps(ev.speed_mbps);
    } else if (ev.kind === "started") {
      setProgressMB(0);
      setSpeedMBps(0);
    } else if (
      ev.kind === "completed" ||
      ev.kind === "failed" ||
      ev.kind === "skipped"
    ) {
      setProgressMB(null);
      setSpeedMBps(null);
    }
  });

  const runSearch = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!query.trim()) return;
    setSearching(true);
    setError(null);
    setResults([]);
    try {
      const resp = await search(query.trim(), "track", 15);
      const list: SearchTrackResult[] = Array.isArray(resp)
        ? resp
        : ((resp as { tracks?: SearchTrackResult[] }).tracks ?? []);
      setResults(list);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "search failed");
    } finally {
      setSearching(false);
    }
  };

  const triggerDownload = async (track: SearchTrackResult) => {
    setDownloading(track.id);
    setDownloadMsg(null);
    setError(null);
    try {
      await downloadTrack({
        service,
        spotify_id: track.id,
        track_name: track.name,
        artist_name: track.artists,
        album_name: track.album_name,
        cover_url: track.images,
        tidal_api_url: service === "tidal" ? tidalAPI : undefined,
        audio_format: service === "qobuz" ? "27" : "LOSSLESS",
        filename_format: "title-artist",
        allow_fallback: true,
      });
      setDownloadMsg(`✓ ${track.name}`);
    } catch (err) {
      setError(
        err instanceof ApiError ? `Download failed: ${err.message}` : "download failed"
      );
    } finally {
      setDownloading(null);
    }
  };

  const headerNote = useMemo(() => {
    if (service === "tidal" && !tidalAPI) {
      return "Tidal benötigt eine eigene API-URL (siehe Hinweis unten).";
    }
    return null;
  }, [service, tidalAPI]);

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100">
      <header className="flex items-center justify-between px-4 py-3 border-b border-zinc-800 sticky top-0 bg-zinc-950/90 backdrop-blur z-10">
        <div>
          <h1 className="text-base font-semibold">SpotiFLAC</h1>
          <p className="text-xs text-zinc-500">
            v{version} · {user.username}
            {user.is_admin ? " (admin)" : ""}
          </p>
        </div>
        <button
          onClick={onLogout}
          className="text-xs rounded-md border border-zinc-700 px-3 py-1.5 hover:bg-zinc-800"
        >
          Logout
        </button>
      </header>

      {downloading !== null && (
        <div className="sticky top-[57px] z-10 bg-emerald-900/30 border-b border-emerald-700/40 px-4 py-2 text-xs flex items-center gap-3">
          <span className="inline-block h-2 w-2 rounded-full bg-emerald-400 animate-pulse" />
          <span className="flex-1 truncate">
            Lade…{" "}
            {progressMB !== null && (
              <span className="tabular-nums text-emerald-300">
                {progressMB.toFixed(1)} MB
                {speedMBps !== null && speedMBps > 0
                  ? ` · ${speedMBps.toFixed(2)} MB/s`
                  : ""}
              </span>
            )}
          </span>
        </div>
      )}

      <main className="max-w-2xl mx-auto px-4 py-4 space-y-4 pb-24">
        <section className="space-y-2">
          <div className="flex gap-2">
            {(["qobuz", "tidal", "amazon"] as const).map((s) => (
              <button
                key={s}
                onClick={() => setService(s)}
                className={`text-xs rounded-md px-3 py-1.5 border ${
                  service === s
                    ? "bg-emerald-600 border-emerald-500 text-white"
                    : "border-zinc-700 hover:bg-zinc-800"
                }`}
              >
                {s}
              </button>
            ))}
          </div>
          {service === "tidal" && (
            <input
              placeholder="https://your-tidal-instance.example/"
              value={tidalAPI}
              onChange={(e) => setTidalAPI(e.target.value)}
              className="w-full rounded-md bg-zinc-900 border border-zinc-800 px-3 py-2 text-xs"
            />
          )}
          {headerNote && <p className="text-xs text-amber-400">{headerNote}</p>}
        </section>

        <form onSubmit={runSearch} className="flex gap-2">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Spotify-Track suchen…"
            className="flex-1 rounded-md bg-zinc-900 border border-zinc-800 px-3 py-2 text-sm focus:outline-none focus:border-zinc-600"
            inputMode="search"
          />
          <button
            type="submit"
            disabled={searching}
            className="rounded-md bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 px-4 text-sm font-medium"
          >
            {searching ? "…" : "Suchen"}
          </button>
        </form>

        {error && (
          <p className="text-sm text-red-400 rounded-md border border-red-900/50 bg-red-950/30 px-3 py-2">
            {error}
          </p>
        )}
        {downloadMsg && (
          <p className="text-sm text-emerald-400 rounded-md border border-emerald-900/50 bg-emerald-950/30 px-3 py-2">
            {downloadMsg}
          </p>
        )}

        <ul className="space-y-2">
          {results.map((track) => (
            <li
              key={track.id}
              className="flex items-center gap-3 rounded-lg border border-zinc-800 bg-zinc-900 p-3"
            >
              {track.images ? (
                <img
                  src={track.images}
                  alt=""
                  className="w-12 h-12 rounded object-cover shrink-0"
                />
              ) : (
                <div className="w-12 h-12 rounded bg-zinc-800 shrink-0" />
              )}
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium truncate">{track.name}</p>
                <p className="text-xs text-zinc-400 truncate">
                  {track.artists}
                  {track.album_name ? ` · ${track.album_name}` : ""}
                </p>
              </div>
              <button
                onClick={() => triggerDownload(track)}
                disabled={downloading !== null}
                className="text-xs rounded-md bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 px-3 py-2 font-medium shrink-0 min-w-[80px] tabular-nums"
              >
                {downloading === track.id
                  ? progressMB !== null
                    ? `${progressMB.toFixed(1)}MB`
                    : "…"
                  : "FLAC"}
              </button>
            </li>
          ))}
        </ul>

        {user.is_admin && <InviteForm />}
      </main>
    </div>
  );
}

function InviteForm() {
  const [open, setOpen] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [msg, setMsg] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg(null);
    try {
      await invite(username.trim(), password);
      setMsg(`User ${username} angelegt.`);
      setUsername("");
      setPassword("");
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "invite failed");
    }
  };

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="text-xs text-zinc-500 hover:text-zinc-300"
      >
        + Nutzer einladen
      </button>
    );
  }
  return (
    <form
      onSubmit={submit}
      className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-900 p-3"
    >
      <p className="text-xs font-medium text-zinc-300">Neuen Nutzer anlegen</p>
      <input
        placeholder="username"
        value={username}
        onChange={(e) => setUsername(e.target.value)}
        className="w-full rounded-md bg-zinc-800 border border-zinc-700 px-3 py-2 text-sm"
        required
      />
      <input
        placeholder="password"
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        className="w-full rounded-md bg-zinc-800 border border-zinc-700 px-3 py-2 text-sm"
        required
      />
      <div className="flex gap-2">
        <button
          type="submit"
          className="text-xs rounded-md bg-emerald-600 hover:bg-emerald-500 px-3 py-1.5"
        >
          Anlegen
        </button>
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="text-xs rounded-md border border-zinc-700 px-3 py-1.5"
        >
          Abbrechen
        </button>
      </div>
      {msg && <p className="text-xs text-zinc-300">{msg}</p>}
    </form>
  );
}
