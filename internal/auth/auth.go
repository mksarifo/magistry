package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const CookieName = "magistry_session"
const lifetime = 24 * time.Hour
const iterations = 600000

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,31}$`)
var ErrConfigured = errors.New("An administrator is already configured")

type account struct {
	Username string `json:"username"`
	Salt     string `json:"salt"`
	Hash     string `json:"passwordHash"`
}
type session struct {
	Username string
	Expires  time.Time
}
type Manager struct {
	mu                   sync.Mutex
	file                 string
	account              account
	envUser, envPassword string
	sessions             map[[32]byte]session
}

func New(file, username, password string) (*Manager, error) {
	if (username == "") != (password == "") {
		return nil, errors.New("Set both AUTH_USERNAME and AUTH_PASSWORD, or neither")
	}
	m := &Manager{file: file, envUser: username, envPassword: password, sessions: make(map[[32]byte]session)}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal(data, &m.account); err != nil {
				return nil, errors.New("Cannot read administrator account")
			}
			salt, saltErr := hex.DecodeString(m.account.Salt)
			hash, hashErr := hex.DecodeString(m.account.Hash)
			if m.account.Username == "" || saltErr != nil || hashErr != nil || len(salt) != 16 || len(hash) != 32 {
				return nil, errors.New("Invalid administrator account file")
			}
		}
	}
	return m, nil
}

func (m *Manager) Configured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.envUser != "" || m.account.Username != ""
}
func (m *Manager) Managed() bool { return m.envUser != "" }

// SaveJSON replaces a settings file atomically and keeps credentials owner-only.
func SaveJSON(file string, value any) error {
	if file == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), file)
}

func derive(password, salt string) []byte {
	decoded, _ := hex.DecodeString(salt)
	key, _ := pbkdf2.Key(sha256.New, password, decoded, iterations, 32)
	return key
}

func (m *Manager) Setup(username, password string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("Username must be 3–32 letters, numbers, dots, hyphens, or underscores")
	}
	if len(password) < 12 || len(password) > 256 {
		return errors.New("Password must be 12–256 bytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.envUser != "" || m.account.Username != "" {
		return ErrConfigured
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	a := account{Username: username, Salt: hex.EncodeToString(salt)}
	a.Hash = hex.EncodeToString(derive(password, a.Salt))
	if err := SaveJSON(m.file, a); err != nil {
		return err
	}
	m.account = a
	return nil
}

func (m *Manager) Verify(username, password string) bool {
	if len(password) > 256 || len(username) > 128 {
		return false
	}
	m.mu.Lock()
	a, envUser, envPassword := m.account, m.envUser, m.envPassword
	m.mu.Unlock()
	u := sha256.Sum256([]byte(username))
	if envUser != "" {
		wantU, wantP, p := sha256.Sum256([]byte(envUser)), sha256.Sum256([]byte(envPassword)), sha256.Sum256([]byte(password))
		return subtle.ConstantTimeCompare(u[:], wantU[:])&subtle.ConstantTimeCompare(p[:], wantP[:]) == 1
	}
	if a.Username == "" {
		return false
	}
	wantU := sha256.Sum256([]byte(a.Username))
	wantP, _ := hex.DecodeString(a.Hash)
	return subtle.ConstantTimeCompare(u[:], wantU[:])&subtle.ConstantTimeCompare(derive(password, a.Salt), wantP) == 1
}

func (m *Manager) Login(username, password string) (string, bool) {
	if !m.Verify(username, password) {
		return "", false
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", false
	}
	token := hex.EncodeToString(value)
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range m.sessions {
		if time.Now().After(s.Expires) {
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= 1024 {
		return "", false
	}
	m.sessions[sha256.Sum256([]byte(token))] = session{Username: username, Expires: time.Now().Add(lifetime)}
	return token, true
}

func (m *Manager) User(r *http.Request, cookies bool) string {
	if cookies {
		if user := m.SessionUser(r); user != "" {
			return user
		}
	}
	if u, p, ok := r.BasicAuth(); ok && m.Verify(u, p) {
		return u
	}
	return ""
}

func (m *Manager) SessionUser(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil {
		key := sha256.Sum256([]byte(c.Value))
		m.mu.Lock()
		s, ok := m.sessions[key]
		if ok && time.Now().After(s.Expires) {
			delete(m.sessions, key)
			ok = false
		}
		m.mu.Unlock()
		if ok {
			return s.Username
		}
	}
	return ""
}

func (m *Manager) Logout(r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, sha256.Sum256([]byte(c.Value)))
		m.mu.Unlock()
	}
}

func SetCookie(w http.ResponseWriter, r *http.Request, token string, secure bool) {
	c := &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: secure || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(lifetime.Seconds()), Expires: time.Now().Add(lifetime)}
	if token == "" {
		c.MaxAge = -1
		c.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, c)
}
