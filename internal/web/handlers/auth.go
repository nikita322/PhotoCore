package handlers

import "net/http"

// Index перенаправляет на галерею
func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/gallery", http.StatusFound)
}

// LoginPage отображает страницу входа
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "login.html", map[string]interface{}{
		"HideHeader": true,
	})
}

// Login обрабатывает вход пользователя
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	session, err := h.auth.Login(username, password)
	if err != nil {
		h.render(w, "login.html", map[string]interface{}{
			"HideHeader": true,
			"Error":      "Неверное имя пользователя или пароль",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieSessionName,
		Value:    session.ID,
		Path:     "/",
		MaxAge:   h.cfg.Auth.SessionMaxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/gallery", http.StatusFound)
}

// Logout выполняет выход пользователя
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(cookieSessionName)
	if err == nil {
		h.auth.Logout(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:   cookieSessionName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/login", http.StatusFound)
}
