package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/afkarxyz/SpotiFLAC/backend"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// --- Search ---

type searchRequest struct {
	Query      string `json:"query"`
	SearchType string `json:"search_type,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}

func handleSearch(c echo.Context) error {
	var req searchRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if strings.TrimSpace(req.Query) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "query required")
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()

	if req.SearchType != "" {
		results, err := backend.SearchSpotifyByType(ctx, req.Query, req.SearchType, req.Limit, req.Offset)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, err.Error())
		}
		return c.JSON(http.StatusOK, results)
	}

	resp, err := backend.SearchSpotify(ctx, req.Query, req.Limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return c.JSON(http.StatusOK, resp)
}

// --- Metadata (one-shot, no streaming for skeleton) ---

type metadataRequest struct {
	URL       string  `json:"url"`
	Batch     bool    `json:"batch,omitempty"`
	Delay     float64 `json:"delay,omitempty"`
	Timeout   float64 `json:"timeout,omitempty"`
	Separator string  `json:"separator,omitempty"`
}

func handleMetadata(c echo.Context) error {
	var req metadataRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if strings.TrimSpace(req.URL) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url required")
	}
	if req.Timeout <= 0 {
		req.Timeout = 300
	}
	if req.Delay <= 0 {
		req.Delay = 1
	}
	separator := req.Separator
	if separator == "" {
		separator = ", "
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), time.Duration(req.Timeout*float64(time.Second)))
	defer cancel()

	data, err := backend.GetFilteredSpotifyData(ctx, req.URL, req.Batch, time.Duration(req.Delay*float64(time.Second)), separator, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return c.JSON(http.StatusOK, data)
}

// --- Download (streams the FLAC back to the browser) ---

type downloadRequest struct {
	Service              string `json:"service"`
	TrackName            string `json:"track_name,omitempty"`
	ArtistName           string `json:"artist_name,omitempty"`
	AlbumName            string `json:"album_name,omitempty"`
	AlbumArtist          string `json:"album_artist,omitempty"`
	ReleaseDate          string `json:"release_date,omitempty"`
	CoverURL             string `json:"cover_url,omitempty"`
	TidalAPIURL          string `json:"tidal_api_url,omitempty"`
	AudioFormat          string `json:"audio_format,omitempty"`
	FilenameFormat       string `json:"filename_format,omitempty"`
	SpotifyID            string `json:"spotify_id,omitempty"`
	EmbedLyrics          bool   `json:"embed_lyrics,omitempty"`
	EmbedMaxQualityCover bool   `json:"embed_max_quality_cover,omitempty"`
	ServiceURL           string `json:"service_url,omitempty"`
	Duration             int    `json:"duration,omitempty"`
	SpotifyTrackNumber   int    `json:"spotify_track_number,omitempty"`
	SpotifyDiscNumber    int    `json:"spotify_disc_number,omitempty"`
	SpotifyTotalTracks   int    `json:"spotify_total_tracks,omitempty"`
	SpotifyTotalDiscs    int    `json:"spotify_total_discs,omitempty"`
	ISRC                 string `json:"isrc,omitempty"`
	Copyright            string `json:"copyright,omitempty"`
	Publisher            string `json:"publisher,omitempty"`
	Composer             string `json:"composer,omitempty"`
	AllowFallback        bool   `json:"allow_fallback,omitempty"`
	UseFirstArtistOnly   bool   `json:"use_first_artist_only,omitempty"`
	UseSingleGenre       bool   `json:"use_single_genre,omitempty"`
	EmbedGenre           bool   `json:"embed_genre,omitempty"`
	Separator            string `json:"separator,omitempty"`
}

// tmpRoot returns the per-user temp directory base. Files inside are streamed out and then deleted.
func tmpRoot(username string) (string, error) {
	base := strings.TrimSpace(os.Getenv("SPOTIFLAC_TMP_DIR"))
	if base == "" {
		base = filepath.Join(os.TempDir(), "spotiflac")
	}
	dir := filepath.Join(base, sanitizeUserDir(username))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func sanitizeUserDir(username string) string {
	out := make([]rune, 0, len(username))
	for _, r := range strings.ToLower(username) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "anon"
	}
	return string(out)
}

// handleDownload runs the existing backend downloader into a per-request temp dir,
// streams the resulting file to the client, then cleans up.
func handleDownload(c echo.Context) error {
	username := CurrentUser(c)

	var req downloadRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if req.Service == "" {
		req.Service = "tidal"
	}
	if req.AudioFormat == "" {
		req.AudioFormat = "LOSSLESS"
	}
	if req.FilenameFormat == "" {
		req.FilenameFormat = "title-artist"
	}

	userTmp, err := tmpRoot(username)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	jobDir := filepath.Join(userTmp, uuid.NewString())
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	defer os.RemoveAll(jobDir)

	filePath, dlErr := runDownload(req, jobDir)
	if dlErr != nil {
		return echo.NewHTTPError(http.StatusBadGateway, dlErr.Error())
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "downloaded file not found")
	}
	if info.Size() == 0 {
		return echo.NewHTTPError(http.StatusInternalServerError, "downloaded file is empty")
	}

	filename := filepath.Base(filePath)
	c.Response().Header().Set(echo.HeaderContentType, mimeFor(filename))
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Response().Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	return c.File(filePath)
}

func runDownload(req downloadRequest, outputDir string) (string, error) {
	spotifyURL := ""
	if req.SpotifyID != "" {
		spotifyURL = fmt.Sprintf("https://open.spotify.com/track/%s", req.SpotifyID)
	}
	separator := req.Separator
	if separator == "" {
		separator = ", "
	}

	switch req.Service {
	case "tidal":
		tidalAPI := strings.TrimSpace(req.TidalAPIURL)
		if !strings.HasPrefix(strings.TrimRight(tidalAPI, "/"), "https://") {
			tidalAPI = backend.GetCustomTidalAPISetting()
		}
		if !strings.HasPrefix(strings.TrimRight(tidalAPI, "/"), "https://") {
			return "", errors.New("a configured HTTPS Tidal instance is required (set SPOTIFLAC_TIDAL_API_URL or per-request tidal_api_url)")
		}
		downloader := backend.NewTidalDownloader(tidalAPI)
		if req.ServiceURL != "" {
			return downloader.DownloadByURL(req.ServiceURL, outputDir, req.AudioFormat, req.FilenameFormat,
				false, 0, req.TrackName, req.ArtistName, req.AlbumName, req.AlbumArtist, req.ReleaseDate,
				false, req.CoverURL, req.EmbedMaxQualityCover,
				req.SpotifyTrackNumber, req.SpotifyDiscNumber, req.SpotifyTotalTracks, req.SpotifyTotalDiscs,
				req.Copyright, req.Publisher, req.Composer, separator, req.ISRC, spotifyURL,
				req.AllowFallback, req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre)
		}
		return downloader.Download(req.SpotifyID, outputDir, req.AudioFormat, req.FilenameFormat,
			false, 0, req.TrackName, req.ArtistName, req.AlbumName, req.AlbumArtist, req.ReleaseDate,
			false, req.CoverURL, req.EmbedMaxQualityCover,
			req.SpotifyTrackNumber, req.SpotifyDiscNumber, req.SpotifyTotalTracks, req.SpotifyTotalDiscs,
			req.Copyright, req.Publisher, req.Composer, separator, req.ISRC, spotifyURL,
			req.AllowFallback, req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre)

	case "qobuz":
		isrc := strings.TrimSpace(req.ISRC)
		if isrc == "" && req.SpotifyID != "" {
			client := backend.NewSongLinkClient()
			if v, err := client.GetISRCDirect(req.SpotifyID); err == nil {
				isrc = v
			}
		}
		if isrc == "" {
			return "", errors.New("ISRC is required for Qobuz downloads")
		}
		downloader := backend.NewQobuzDownloader()
		quality := req.AudioFormat
		if quality == "" {
			quality = "6"
		}
		return downloader.DownloadTrackWithISRC(isrc, outputDir, quality, req.FilenameFormat,
			false, 0, req.TrackName, req.ArtistName, req.AlbumName, req.AlbumArtist, req.ReleaseDate,
			false, req.CoverURL, req.EmbedMaxQualityCover,
			req.SpotifyTrackNumber, req.SpotifyDiscNumber, req.SpotifyTotalTracks, req.SpotifyTotalDiscs,
			req.Copyright, req.Publisher, req.Composer, separator, spotifyURL,
			req.AllowFallback, req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre)

	case "amazon":
		downloader := backend.NewAmazonDownloader()
		if req.ServiceURL != "" {
			return downloader.DownloadByURL(req.ServiceURL, outputDir, req.AudioFormat, req.FilenameFormat,
				"", "", false, 0,
				req.TrackName, req.ArtistName, req.AlbumName, req.AlbumArtist, req.ReleaseDate, req.CoverURL,
				req.SpotifyTrackNumber, req.SpotifyDiscNumber, req.SpotifyTotalTracks, req.EmbedMaxQualityCover, req.SpotifyTotalDiscs,
				req.Copyright, req.Publisher, req.Composer, separator, req.ISRC, spotifyURL,
				req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre)
		}
		return downloader.DownloadBySpotifyID(req.SpotifyID, outputDir, req.AudioFormat, req.FilenameFormat,
			"", "", false, 0,
			req.TrackName, req.ArtistName, req.AlbumName, req.AlbumArtist, req.ReleaseDate, req.CoverURL,
			req.SpotifyTrackNumber, req.SpotifyDiscNumber, req.SpotifyTotalTracks, req.EmbedMaxQualityCover, req.SpotifyTotalDiscs,
			req.Copyright, req.Publisher, req.Composer, separator, req.ISRC, spotifyURL,
			req.UseFirstArtistOnly, req.UseSingleGenre, req.EmbedGenre)

	default:
		return "", fmt.Errorf("unknown service: %s", req.Service)
	}
}

func mimeFor(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".flac":
		return "audio/flac"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	default:
		return "application/octet-stream"
	}
}

// --- History (per-user via app-name namespacing of the existing bucket scheme) ---

func handleHistoryList(c echo.Context) error {
	username := CurrentUser(c)
	items, err := backend.GetHistoryItems(historyNamespaceFor(username))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if items == nil {
		items = []backend.HistoryItem{}
	}
	return c.JSON(http.StatusOK, items)
}

func historyNamespaceFor(username string) string {
	return "user:" + sanitizeUserDir(username)
}

// --- Health & info ---

func handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"ok":      true,
		"version": backend.AppVersion,
		"time":    time.Now().Unix(),
	})
}
