package app

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"bidos/internal/store"
)

const sessionTTL = 12 * time.Hour

// Login opens a session for a user (demo login: no password; the user list is the org's).
func (a *App) Login(orgID, userID string) (string, error) {
	u, err := a.DB.GetUser(userID)
	if err != nil || u.OrgID != orgID {
		return "", userErr("That user does not exist in this workspace.")
	}
	id := store.NewID() + store.NewID()
	if err := a.DB.CreateSession(store.Session{ID: id, UserID: u.ID, OrgID: orgID, ExpiresAt: store.FormatTime(time.Now().Add(sessionTTL))}); err != nil {
		return "", err
	}
	a.Activity(orgID, u.ID, "login", "user", u.ID, nil)
	return id, nil
}

// LoginPassword authenticates with email + password (live mode).
func (a *App) LoginPassword(orgID, email, password string) (string, error) {
	u, err := a.DB.UserByEmail(orgID, email)
	if err != nil || u.PasswordHash == "" || !VerifyPassword(u.PasswordHash, password) {
		return "", userErr("Email or password is incorrect.")
	}
	return a.Login(orgID, u.ID)
}

// SetPassword stores a PBKDF2-SHA256 hash.
func (a *App) SetPassword(userID, password string) error {
	if len(password) < 8 {
		return userErr("Passwords must be at least 8 characters.")
	}
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	return a.DB.Exec("UPDATE users SET password_hash = ? WHERE id = ?", h, userID)
}

// HashPassword returns "pbkdf2$<iter>$<salt>$<hash>".
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const iter = 210000
	dk, err := pbkdf2.Key(sha256.New, password, salt, iter, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2$210000$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(dk), nil
}

// VerifyPassword checks a password against a stored hash.
func VerifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	var iter int
	for _, c := range parts[1] {
		iter = iter*10 + int(c-'0')
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return hmac.Equal(dk, want)
}

// SessionUser resolves a session id to its user and organization.
func (a *App) SessionUser(sessionID string) (store.User, store.Organization, bool) {
	if sessionID == "" {
		return store.User{}, store.Organization{}, false
	}
	s, err := a.DB.GetSession(sessionID)
	if err != nil || s.ExpiresAt < store.Now() {
		return store.User{}, store.Organization{}, false
	}
	u, err := a.DB.GetUser(s.UserID)
	if err != nil {
		return store.User{}, store.Organization{}, false
	}
	org, err := a.DB.GetOrg(s.OrgID)
	if err != nil {
		return store.User{}, store.Organization{}, false
	}
	return u, org, true
}

// Logout ends a session.
func (a *App) Logout(sessionID string) { _ = a.DB.DeleteSession(sessionID) }

// RBAC ---------------------------------------------------------------------------------

var permissions = map[string][]string{
	store.RoleAdmin:           {"*"},
	store.RoleProposalManager: {"*"},
	store.RoleWriter:          {"bid.create", "bid.edit", "doc.upload", "req.edit", "answer.generate", "answer.edit", "answer.submit", "task.respond", "qa.run", "export.run", "library.edit", "proposal.edit", "clarification.edit", "pipeline.run"},
	store.RoleSME:             {"task.respond", "answer.edit", "doc.upload"},
	store.RoleReviewer:        {"answer.approve", "answer.reject", "qa.run", "qa.resolve", "export.run", "gate.approve", "req.confirm", "answer.edit", "answer.submit"},
	store.RoleViewer:          {},
}

// Can reports whether a role may perform an action.
func Can(role, action string) bool {
	for _, p := range permissions[role] {
		if p == "*" || p == action {
			return true
		}
	}
	return false
}
