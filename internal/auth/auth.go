package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/storage"
)

// Context key для хранения сессии
type contextKey string

const SessionKey contextKey = "session"

// GetSession извлекает сессию из контекста запроса
func GetSession(r *http.Request) *storage.Session {
	if sess, ok := r.Context().Value(SessionKey).(*storage.Session); ok {
		return sess
	}
	return nil
}

// GetUserID возвращает ID пользователя из сессии
func GetUserID(r *http.Request) string {
	if sess := GetSession(r); sess != nil {
		return sess.UserID
	}
	return ""
}

// GetUserRole возвращает роль пользователя из сессии
func GetUserRole(r *http.Request) string {
	if sess := GetSession(r); sess != nil {
		return sess.Role
	}
	return ""
}

// === Permission helpers ===

// CanDeleteMedia проверяет право на удаление медиа (только admin)
func CanDeleteMedia(role string) bool {
	return role == storage.RoleAdmin
}

// CanDeleteAlbum проверяет право на удаление альбома (только admin)
func CanDeleteAlbum(role string) bool {
	return role == storage.RoleAdmin
}

// CanCreateAlbum проверяет право на создание альбома (admin и editor)
func CanCreateAlbum(role string) bool {
	return role == storage.RoleAdmin || role == storage.RoleEditor
}

// CanEditAlbum проверяет право на редактирование альбома (admin и editor)
func CanEditAlbum(role string) bool {
	return role == storage.RoleAdmin || role == storage.RoleEditor
}

// CanEdit проверяет право на редактирование (admin и editor)
func CanEdit(role string) bool {
	return role == storage.RoleAdmin || role == storage.RoleEditor
}

// CanManageTags проверяет право на добавление тегов (admin и editor)
func CanManageTags(role string) bool {
	return role == storage.RoleAdmin || role == storage.RoleEditor
}

// CanDeleteTags проверяет право на удаление тегов (только admin)
func CanDeleteTags(role string) bool {
	return role == storage.RoleAdmin
}

// CanManageUsers проверяет право на управление пользователями (только admin)
func CanManageUsers(role string) bool {
	return role == storage.RoleAdmin
}

// IsAdmin проверяет, является ли пользователь администратором
func IsAdmin(role string) bool {
	return role == storage.RoleAdmin
}

// Auth управляет аутентификацией пользователей
type Auth struct {
	cfg   *config.Config
	store *storage.Store
}

// NewAuth создает новый сервис аутентификации
func NewAuth(cfg *config.Config, store *storage.Store) *Auth {
	return &Auth{
		cfg:   cfg,
		store: store,
	}
}

// HashPassword хеширует пароль
func (a *Auth) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// EnsureAdminUser создает администратора если его нет
func (a *Auth) EnsureAdminUser() error {
	// Проверяем, существует ли админ
	user, err := a.store.GetUser(a.cfg.Auth.AdminUsername)
	if err != nil {
		return fmt.Errorf("failed to check admin user: %w", err)
	}

	if user != nil {
		return nil // Админ уже существует
	}

	// Создаем нового админа
	hash, err := bcrypt.GenerateFromPassword([]byte(a.cfg.Auth.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	user = &storage.User{
		ID:           generateID(),
		Username:     a.cfg.Auth.AdminUsername,
		PasswordHash: string(hash),
		Role:         "admin",
		CreatedAt:    time.Now(),
	}

	if err := a.store.SaveUser(user); err != nil {
		return fmt.Errorf("failed to save admin user: %w", err)
	}

	return nil
}

// Login выполняет аутентификацию пользователя
func (a *Auth) Login(username, password string) (*storage.Session, error) {
	user, err := a.store.GetUser(username)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	// Обновляем время последнего входа
	user.LastLogin = time.Now()
	if err := a.store.SaveUser(user); err != nil {
		// Не критично, продолжаем
	}

	// Создаем сессию
	session := &storage.Session{
		ID:        generateID(),
		UserID:    user.ID,
		Username:  user.Username,
		Role:      user.Role,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Duration(a.cfg.Auth.SessionMaxAge) * time.Second),
	}

	if err := a.store.SaveSession(session); err != nil {
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	return session, nil
}

// Logout завершает сессию пользователя
func (a *Auth) Logout(sessionID string) error {
	return a.store.DeleteSession(sessionID)
}

// ValidateSession проверяет валидность сессии
func (a *Auth) ValidateSession(sessionID string) (*storage.Session, error) {
	session, err := a.store.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	// Проверяем срок действия
	if time.Now().After(session.ExpiresAt) {
		a.store.DeleteSession(sessionID)
		return nil, nil
	}

	return session, nil
}

// Middleware проверяет аутентификацию для защищенных маршрутов
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		session, err := a.ValidateSession(cookie.Value)
		if err != nil || session == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		// Добавляем сессию в контекст запроса
		ctx := context.WithValue(r.Context(), SessionKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole создает middleware для проверки роли
func (a *Auth) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session := GetSession(r)
			if session == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			allowed := false
			for _, role := range roles {
				if session.Role == role {
					allowed = true
					break
				}
			}

			if !allowed {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func generateID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
