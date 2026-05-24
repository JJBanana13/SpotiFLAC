package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/afkarxyz/SpotiFLAC/backend"
	"github.com/labstack/echo/v4"
	"go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

const (
	usersDBFile     = "users.db"
	usersBucket     = "users"
	sessionCookie   = "spotiflac_session"
	sessionDuration = 30 * 24 * time.Hour
	contextUserKey  = "spotiflac.user"
)

type User struct {
	Username  string `json:"username"`
	Hash      []byte `json:"hash"`
	CreatedAt int64  `json:"created_at"`
	IsAdmin   bool   `json:"is_admin"`
}

var (
	usersDB     *bbolt.DB
	usersDBOnce sync.Once
	cookieKey   []byte
)

func initCookieKey() {
	raw := strings.TrimSpace(os.Getenv("SPOTIFLAC_COOKIE_SECRET"))
	if raw == "" {
		// Ephemeral key — sessions reset on each restart.
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		cookieKey = key
		fmt.Println("[auth] WARNING: SPOTIFLAC_COOKIE_SECRET not set — generated ephemeral key. Sessions will be invalidated on restart.")
		return
	}
	if len(raw) < 32 {
		fmt.Println("[auth] WARNING: SPOTIFLAC_COOKIE_SECRET shorter than 32 chars; consider using a longer secret.")
	}
	cookieKey = []byte(raw)
}

func InitAuth() error {
	initCookieKey()

	var initErr error
	usersDBOnce.Do(func() {
		appDir, err := backend.EnsureAppDir()
		if err != nil {
			initErr = err
			return
		}
		dbPath := appDir + string(os.PathSeparator) + usersDBFile
		db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{Timeout: time.Second})
		if err != nil {
			initErr = fmt.Errorf("open users db: %w", err)
			return
		}
		if err := db.Update(func(tx *bbolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists([]byte(usersBucket))
			return err
		}); err != nil {
			initErr = err
			return
		}
		usersDB = db
	})
	if initErr != nil {
		return initErr
	}

	// Seed initial user from env if needed.
	initialUser := strings.TrimSpace(os.Getenv("SPOTIFLAC_INITIAL_USER"))
	initialPassword := os.Getenv("SPOTIFLAC_INITIAL_PASSWORD")
	if initialUser != "" && initialPassword != "" {
		exists, err := userExists(initialUser)
		if err != nil {
			return err
		}
		if !exists {
			if err := createUser(initialUser, initialPassword, true); err != nil {
				return fmt.Errorf("seed initial user: %w", err)
			}
			fmt.Printf("[auth] seeded initial admin user %q\n", initialUser)
		}
	}

	return nil
}

func CloseAuth() {
	if usersDB != nil {
		_ = usersDB.Close()
	}
}

func userExists(username string) (bool, error) {
	var exists bool
	err := usersDB.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		exists = b.Get([]byte(strings.ToLower(username))) != nil
		return nil
	})
	return exists, err
}

func createUser(username, password string, admin bool) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return errors.New("username and password required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := User{
		Username:  username,
		Hash:      hash,
		CreatedAt: time.Now().Unix(),
		IsAdmin:   admin,
	}
	payload, err := json.Marshal(user)
	if err != nil {
		return err
	}
	return usersDB.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		return b.Put([]byte(strings.ToLower(username)), payload)
	})
}

func getUser(username string) (*User, error) {
	var user *User
	err := usersDB.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		raw := b.Get([]byte(strings.ToLower(username)))
		if raw == nil {
			return nil
		}
		var u User
		if err := json.Unmarshal(raw, &u); err != nil {
			return err
		}
		user = &u
		return nil
	})
	return user, err
}

// signCookie produces "value|expiry|hmac" so the server can validate it without state.
func signCookie(username string, expiry time.Time) string {
	payload := fmt.Sprintf("%s|%d", strings.ToLower(username), expiry.Unix())
	mac := hmac.New(sha256.New, cookieKey)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "|" + sig
}

func verifyCookie(raw string) (string, error) {
	parts := strings.Split(raw, "|")
	if len(parts) != 3 {
		return "", errors.New("malformed cookie")
	}
	username, expiryStr, sig := parts[0], parts[1], parts[2]
	expectedSig := func() string {
		payload := username + "|" + expiryStr
		mac := hmac.New(sha256.New, cookieKey)
		mac.Write([]byte(payload))
		return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}()
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", errors.New("invalid signature")
	}
	var expiry int64
	if _, err := fmt.Sscanf(expiryStr, "%d", &expiry); err != nil {
		return "", err
	}
	if time.Now().Unix() > expiry {
		return "", errors.New("expired")
	}
	return username, nil
}

func setSessionCookie(c echo.Context, username string) {
	expiry := time.Now().Add(sessionDuration)
	c.SetCookie(&http.Cookie{
		Name:     sessionCookie,
		Value:    signCookie(username, expiry),
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		Secure:   c.Request().TLS != nil || strings.EqualFold(c.Request().Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
	})
}

func CurrentUser(c echo.Context) string {
	if u, ok := c.Get(contextUserKey).(string); ok {
		return u
	}
	return ""
}

// RequireAuth is an echo middleware that resolves the session cookie and 401s otherwise.
func RequireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		cookie, err := c.Cookie(sessionCookie)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
		}
		username, err := verifyCookie(cookie.Value)
		if err != nil {
			clearSessionCookie(c)
			return echo.NewHTTPError(http.StatusUnauthorized, "session invalid")
		}
		c.Set(contextUserKey, username)
		return next(c)
	}
}

// --- HTTP handlers ---

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func handleLogin(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if req.Username == "" || req.Password == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username and password required")
	}
	user, err := getUser(req.Username)
	if err != nil || user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword(user.Hash, []byte(req.Password)); err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}
	setSessionCookie(c, user.Username)
	return c.JSON(http.StatusOK, map[string]any{
		"username": user.Username,
		"is_admin": user.IsAdmin,
	})
}

func handleLogout(c echo.Context) error {
	clearSessionCookie(c)
	return c.NoContent(http.StatusNoContent)
}

func handleMe(c echo.Context) error {
	username := CurrentUser(c)
	user, err := getUser(username)
	if err != nil || user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"username": user.Username,
		"is_admin": user.IsAdmin,
	})
}

type inviteRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func handleInvite(c echo.Context) error {
	currentUsername := CurrentUser(c)
	currentUser, err := getUser(currentUsername)
	if err != nil || currentUser == nil || !currentUser.IsAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "admin only")
	}
	var req inviteRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if req.Username == "" || req.Password == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username and password required")
	}
	exists, err := userExists(req.Username)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if exists {
		return echo.NewHTTPError(http.StatusConflict, "user already exists")
	}
	if err := createUser(req.Username, req.Password, false); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, map[string]string{"username": req.Username})
}
