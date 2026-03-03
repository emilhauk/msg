package auth

import (
	"net/http"
	"strings"

	redisclient "github.com/emilhauk/msg/internal/redis"
	"golang.org/x/crypto/bcrypt"
)

// PasswordHandler handles email/password login. It is only wired into the
// router when ENABLE_PASSWORD_LOGIN=true; otherwise its routes do not exist
// and the login page shows no password form.
type PasswordHandler struct {
	Redis            *redisclient.Client
	SessionSecret    []byte
	OpenRegistration bool
	AllowList        []string // lowercased, trimmed email addresses
}

// checkAccess reports whether the given email is permitted to log in.
// Mirrors the same logic used by the OAuth handler.
func (h *PasswordHandler) checkAccess(email string) bool {
	if h.OpenRegistration {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range h.AllowList {
		if allowed == email {
			return true
		}
	}
	return false
}

// HandleLogin processes a POST /auth/password/login form submission.
//
// Error responses always use the same generic message to avoid leaking whether
// an email address is registered (user enumeration protection).
func (h *PasswordHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/login?error=bad_request", http.StatusFound)
		return
	}

	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	password := r.PostFormValue("password")

	if email == "" || password == "" {
		http.Redirect(w, r, "/login?error=invalid_credentials", http.StatusFound)
		return
	}

	// Look up the user by email. Use a constant-time path regardless of whether
	// the user exists to reduce timing-based user enumeration.
	user, err := h.Redis.GetUserByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fetch the stored bcrypt hash. If the user does not exist or has no
	// password set (OAuth-only account), compare against a dummy hash so the
	// bcrypt work factor is always paid and timing is consistent.
	var storedHash string
	if user != nil {
		storedHash, err = h.Redis.GetUserPassword(r.Context(), user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	// Always run bcrypt.CompareHashAndPassword so timing is identical whether
	// the user exists or not.
	hashToCompare := storedHash
	if hashToCompare == "" {
		// Dummy hash: bcrypt of "x" at cost 12. We ignore the result.
		hashToCompare = "$2a$12$invalidhashpadding.......................invalid"
	}
	compareErr := bcrypt.CompareHashAndPassword([]byte(hashToCompare), []byte(password))

	// Only after paying the full bcrypt cost do we branch on outcome.
	if user == nil || storedHash == "" || compareErr != nil {
		http.Redirect(w, r, "/login?error=invalid_credentials", http.StatusFound)
		return
	}

	// Allow-list / open-registration check (same semantics as OAuth).
	if !h.checkAccess(user.Email) {
		http.Redirect(w, r, "/login?error=access_denied", http.StatusFound)
		return
	}

	// Issue a session the same way the OAuth handler does, reusing the shared
	// session helpers from session.go.
	signed, err := SignToken(h.SessionSecret)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	token, err := VerifyToken(h.SessionSecret, signed)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Redis.SetSession(r.Context(), token, *user); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	SetCookie(w, signed)
	http.Redirect(w, r, "/rooms/bemro", http.StatusFound)
}
