package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/cache"
	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
	"github.com/photocore/photocore/internal/worker"
)

// ResponseStatus представляет статус JSON-ответа
type ResponseStatus string

const (
	StatusStarted      ResponseStatus = "started"
	StatusDeleted      ResponseStatus = "deleted"
	StatusAdded        ResponseStatus = "added"
	StatusRemoved      ResponseStatus = "removed"
	StatusUpdated      ResponseStatus = "updated"
	StatusRestored     ResponseStatus = "restored"
	StatusMovedToTrash ResponseStatus = "moved_to_trash"
	StatusEmptied      ResponseStatus = "emptied"
	StatusReplaced     ResponseStatus = "replaced"
	StatusUnmarked     ResponseStatus = "unmarked"
	StatusRevoked      ResponseStatus = "revoked"
)

const (
	hxRequestHeader    = "HX-Request"
	hxRequestTrue      = "true"
	contentTypeJSON    = "application/json"
	contentDisposition = "attachment; filename=\"photos.zip\""
	cookieSessionName  = "session"
	uploadDir          = "upload"
	defaultDeviceName  = "Unnamed Device"
	maxUploadSize      = 10 << 30 // 10 GB
	cacheControlDay    = 86400    // 1 day in seconds
)

// Handlers содержит все HTTP-обработчики
type Handlers struct {
	cfg           *config.Config
	store         *storage.Store
	scanner       *scanner.Scanner
	thumbGen      *media.ThumbnailGenerator
	auth          *auth.Auth
	pageTemplates map[string]*template.Template
	cache         *cache.MediaCache
	poolManager   *worker.PoolManager
	thumbService  *worker.ThumbnailService
	buildVersion  string
}

// NewHandlers создает новый экземпляр обработчиков
func NewHandlers(
	cfg *config.Config,
	store *storage.Store,
	scanner *scanner.Scanner,
	thumbGen *media.ThumbnailGenerator,
	auth *auth.Auth,
	pageTemplates map[string]*template.Template,
	mediaCache *cache.MediaCache,
	poolManager *worker.PoolManager,
	thumbService *worker.ThumbnailService,
	buildVersion string,
) *Handlers {
	return &Handlers{
		cfg:           cfg,
		store:         store,
		scanner:       scanner,
		thumbGen:      thumbGen,
		auth:          auth,
		pageTemplates: pageTemplates,
		cache:         mediaCache,
		poolManager:   poolManager,
		thumbService:  thumbService,
		buildVersion:  buildVersion,
	}
}

// baseData возвращает общие данные для шаблонов (сессия, права)
func (h *Handlers) baseData(r *http.Request) map[string]interface{} {
	data := make(map[string]interface{})
	data["BuildVersion"] = h.buildVersion

	if session := auth.GetSession(r); session != nil {
		data["Username"] = session.Username
		data["Role"] = session.Role
		data["IsAdmin"] = session.Role == storage.RoleAdmin
		data["CanEdit"] = session.Role == storage.RoleAdmin || session.Role == storage.RoleEditor

		if favIDs, err := h.store.GetUserFavorites(session.UserID); err == nil {
			favSet := make(map[string]bool, len(favIDs))
			for _, id := range favIDs {
				favSet[id] = true
			}
			data["FavSet"] = favSet
		}
	}
	return data
}

// wantsHTML проверяет, запрашивает ли клиент HTML (браузер или HTMX)
func (h *Handlers) wantsHTML(r *http.Request) bool {
	if r.Header.Get(hxRequestHeader) == hxRequestTrue {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

func (h *Handlers) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := h.pageTemplates[name]
	if !ok {
		logger.InfoLog.Printf("Template not found: %s", name)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		logger.ErrorLog.Printf("Template execution error for %s: %v", name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func (h *Handlers) renderPartial(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := h.pageTemplates[name]
	if !ok {
		logger.ErrorLog.Printf("Template not found: %s", name)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		logger.ErrorLog.Printf("Template execution error for %s: %v", name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func (h *Handlers) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", contentTypeJSON)
	json.NewEncoder(w).Encode(data)
}

func (h *Handlers) jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handlers) getMimeType(ext string) string {
	mimeTypes := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".heic": "image/heic",
		".mp4":  "video/mp4",
		".mov":  "video/quicktime",
		".avi":  "video/x-msvideo",
		".mkv":  "video/x-matroska",
		".webm": "video/webm",
		".raw":  "image/x-raw",
		".cr2":  "image/x-canon-cr2",
		".cr3":  "image/x-canon-cr3",
		".nef":  "image/x-nikon-nef",
		".arw":  "image/x-sony-arw",
		".dng":  "image/x-adobe-dng",
		".orf":  "image/x-olympus-orf",
		".raf":  "image/x-fuji-raf",
		".rw2":  "image/x-panasonic-rw2",
	}
	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

func generateRandomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
