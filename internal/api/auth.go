package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/olimpiacloud/agora/internal/core"
)

type Principal struct {
	Admin  bool
	Cursor *core.Cursor
	Viewer *ViewerClaims
}

type ViewerClaims struct {
	Workspace string `json:"ws"`
	Control   bool   `json:"ctl"`
	Exp       int64  `json:"exp"`
	Nonce     string `json:"n"`
}

type Auth struct {
	admin string
	key   []byte
	m     *core.Manager
}

func LoadOrCreateSecret(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(b))
		if len(s) >= 32 {
			return s, nil
		}
	}
	s := core.NewSecret()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(s+"\n"), 0600); err != nil {
		return "", err
	}
	return s, nil
}

func NewAuth(home string, m *core.Manager) (*Auth, error) {
	admin, err := LoadOrCreateSecret(filepath.Join(home, "admin.token"))
	if err != nil {
		return nil, err
	}
	key, err := LoadOrCreateSecret(filepath.Join(home, "viewer.key"))
	if err != nil {
		return nil, err
	}
	return &Auth{admin: admin, key: []byte(key), m: m}, nil
}

func (a *Auth) MintViewer(ws string, control bool, ttl time.Duration) string {
	c := ViewerClaims{Workspace: ws, Control: control, Exp: time.Now().Add(ttl).Unix(), Nonce: core.NewSecret()[:8]}
	b, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(p))
	return "v1." + p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) parseViewer(tok string) (*ViewerClaims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return nil, errors.New("bad token")
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("bad signature")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c ViewerClaims
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if time.Now().Unix() > c.Exp {
		return nil, errors.New("expired")
	}
	return &c, nil
}

func (a *Auth) Resolve(r *http.Request, allowQuery bool) (*Principal, error) {
	tok := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	if tok == "" && allowQuery {
		tok = r.URL.Query().Get("token")
		if tok != "" && !strings.HasPrefix(tok, "v1.") {
			return nil, errors.New("only viewer tokens are accepted in the query string")
		}
	}
	if tok == "" {
		return nil, errors.New("missing token")
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(a.admin)) == 1 {
		p := &Principal{Admin: true}
		if cid := r.Header.Get("X-Agora-Cursor"); cid != "" {
			c, err := a.m.GetCursor(cid)
			if err != nil {
				return nil, err
			}
			p.Cursor = c
		}
		return p, nil
	}
	if strings.HasPrefix(tok, "v1.") {
		c, err := a.parseViewer(tok)
		if err != nil {
			return nil, err
		}
		return &Principal{Viewer: c}, nil
	}
	if c := a.m.CursorForToken(tok); c != nil {
		return &Principal{Cursor: c}, nil
	}
	return nil, errors.New("invalid token")
}

func (p *Principal) CursorID() string {
	if p.Cursor != nil {
		return p.Cursor.ID
	}
	return ""
}

func (p *Principal) limited() bool {
	return !p.Admin && p.Cursor != nil
}

func (p *Principal) CanWorkspace(ws string) bool {
	if p.Admin {
		return true
	}
	if p.Viewer != nil {
		return p.Viewer.Workspace == ws
	}
	if p.Cursor != nil {
		return p.Cursor.Workspace == ws
	}
	return false
}
