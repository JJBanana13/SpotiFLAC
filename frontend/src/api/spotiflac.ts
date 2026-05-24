import { api, postDownload } from "./client";

// --- types (loosely typed for skeleton; tighten as needed) ---

export interface AuthInfo {
  username: string;
  is_admin: boolean;
}

// Matches backend.SearchResult.
export interface SearchTrackResult {
  id: string;
  name: string;
  type: string;
  artists?: string;
  album_name?: string;
  images?: string;
  release_date?: string;
  external_urls?: string;
  duration_ms?: number;
  total_tracks?: number;
  owner?: string;
  is_explicit?: boolean;
}

export interface SearchResponse {
  tracks?: SearchTrackResult[];
  albums?: SearchTrackResult[];
  artists?: SearchTrackResult[];
  playlists?: SearchTrackResult[];
}

export interface DownloadParams {
  service?: "tidal" | "qobuz" | "amazon";
  spotify_id?: string;
  service_url?: string;
  track_name?: string;
  artist_name?: string;
  album_name?: string;
  cover_url?: string;
  tidal_api_url?: string;
  audio_format?: string;
  filename_format?: string;
  isrc?: string;
  allow_fallback?: boolean;
  embed_max_quality_cover?: boolean;
}

// --- endpoints ---

export const login = (username: string, password: string) =>
  api.post<AuthInfo>("/api/auth/login", { username, password });

export const logout = () => api.post<void>("/api/auth/logout");

export const me = () => api.get<AuthInfo>("/api/auth/me");

export const invite = (username: string, password: string) =>
  api.post<{ username: string }>("/api/auth/invite", { username, password });

export const search = (query: string, searchType = "track", limit = 10) =>
  api.post<SearchTrackResult[] | SearchResponse>("/api/search", {
    query,
    search_type: searchType,
    limit,
  });

export const downloadTrack = (params: DownloadParams) =>
  postDownload("/api/download", params);

export const health = () =>
  api.get<{ ok: boolean; version: string; time: number }>("/api/health");
