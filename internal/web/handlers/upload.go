package handlers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
)

// UploadPage отображает страницу загрузки
func (h *Handlers) UploadPage(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r)

	ua := r.Header.Get("User-Agent")
	isMobile := strings.Contains(strings.ToLower(ua), "mobile") ||
		strings.Contains(strings.ToLower(ua), "android") ||
		strings.Contains(strings.ToLower(ua), "iphone")

	data["CanCapture"] = isMobile

	h.render(w, "upload.html", data)
}

// PWASettingsPage отображает страницу настроек PWA
func (h *Handlers) PWASettingsPage(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r)
	h.render(w, "pwa_settings.html", data)
}

// UploadMedia обрабатывает загрузку медиа-файлов через API
func (h *Handlers) UploadMedia(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	err := r.ParseMultipartForm(maxUploadSize)
	if err != nil {
		h.jsonError(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		h.jsonError(w, "No files provided", http.StatusBadRequest)
		return
	}

	extensions := make(map[string]storage.MediaType)
	for _, ext := range h.cfg.Scan.Extensions.Images {
		extensions[strings.ToLower(ext)] = storage.MediaTypeImage
	}
	for _, ext := range h.cfg.Scan.Extensions.Videos {
		extensions[strings.ToLower(ext)] = storage.MediaTypeVideo
	}
	for _, ext := range h.cfg.Scan.Extensions.Raw {
		extensions[strings.ToLower(ext)] = storage.MediaTypeRaw
	}

	now := time.Now()
	baseDir := h.cfg.Storage.MediaPaths[0]
	year := strconv.Itoa(now.Year())
	month := fmt.Sprintf("%02d", now.Month())
	targetDir := filepath.Join(baseDir, uploadDir, year, month)

	if err := os.MkdirAll(targetDir, storage.DefaultDirPerm); err != nil {
		h.jsonError(w, "Failed to create upload directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var uploaded int
	var errors int
	var mediaIDs []string
	var messages []string

	for _, fileHeader := range files {
		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		mediaType, ok := extensions[ext]
		if !ok {
			messages = append(messages, "Skipped "+fileHeader.Filename+": unsupported file type")
			errors++
			continue
		}

		timestamp := fmt.Sprintf("%04d%02d%02d_%02d%02d%02d", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())
		uniqueFilename := timestamp + "_" + fileHeader.Filename
		targetPath := filepath.Join(targetDir, uniqueFilename)

		src, err := fileHeader.Open()
		if err != nil {
			messages = append(messages, "Failed to open "+fileHeader.Filename+": "+err.Error())
			errors++
			continue
		}

		// Читаем файл в память для проверки exact-duplicate до записи на диск
		var buf bytes.Buffer
		_, err = io.Copy(&buf, src)
		src.Close()
		if err != nil {
			messages = append(messages, "Failed to read "+fileHeader.Filename+": "+err.Error())
			errors++
			continue
		}

		// Проверяем exact duplicate по SHA256 до записи на диск
		checksum, err := scanner.CalculateChecksum(bytes.NewReader(buf.Bytes()))
		if err != nil {
			messages = append(messages, "Failed to calculate checksum for "+fileHeader.Filename+": "+err.Error())
			errors++
			continue
		}

		exactID, err := h.store.ChecksumExists(checksum)
		if err == nil && exactID != "" {
			existing, _ := h.store.GetMedia(exactID)
			isDuplicate := false
			if existing != nil {
				if h.cfg.Scan.DuplicateCheckOriginalExists {
					if _, err := os.Stat(existing.Path); err == nil {
						isDuplicate = true
					}
				} else {
					isDuplicate = true
				}
			}
			if isDuplicate {
				messages = append(messages, "Duplicate skipped: "+uniqueFilename+" (exact copy of "+existing.Filename+")")
				continue
			}
		}

		// Записываем файл на диск
		dst, err := os.Create(targetPath)
		if err != nil {
			messages = append(messages, "Failed to create "+uniqueFilename+": "+err.Error())
			errors++
			continue
		}
		_, err = io.Copy(dst, bytes.NewReader(buf.Bytes()))
		dst.Close()
		if err != nil {
			os.Remove(targetPath)
			messages = append(messages, "Failed to save "+fileHeader.Filename+": "+err.Error())
			errors++
			continue
		}

		fileInfo, err := os.Stat(targetPath)
		if err != nil {
			os.Remove(targetPath)
			messages = append(messages, "Failed to stat "+uniqueFilename+": "+err.Error())
			errors++
			continue
		}

		relPath := filepath.Join(uploadDir, year, month, uniqueFilename)
		mediaItem := &storage.Media{
			ID:         storage.GenerateID(targetPath),
			Path:       targetPath,
			RelPath:    relPath,
			Dir:        filepath.Dir(relPath),
			Filename:   uniqueFilename,
			Ext:        ext,
			Type:       mediaType,
			MimeType:   h.getMimeType(ext),
			Size:       fileInfo.Size(),
			ModifiedAt: fileInfo.ModTime(),
			CreatedAt:  now,
			Checksum:   checksum,
		}

		if mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw {
			if err := scanner.ExtractMetadata(targetPath, mediaItem); err != nil {
				logger.InfoLog.Printf("Warning: failed to extract metadata from %s: %v", uniqueFilename, err)
			}
		}

		isImage := mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw
		if isImage {
			imgHash, err := scanner.CalculateImageHash(targetPath, h.cfg.Tools.Dcraw)
			if err != nil {
				logger.InfoLog.Printf("Warning: failed to calculate image hash for %s: %v", uniqueFilename, err)
			} else {
				mediaItem.ImageHash = imgHash
			}
		}

		if h.cfg.Scan.EnableDuplicateDetection {
			dupResult, err := h.store.CheckDuplicate(
				mediaItem.Size,
				mediaItem.Checksum,
				mediaItem.ImageHash,
				isImage,
				h.cfg.Scan.DuplicateSimilarityThreshold,
				h.cfg.Scan.DuplicateCheckOriginalExists,
			)
			if err == nil && dupResult != nil && dupResult.IsDuplicate {
				mediaItem.DuplicateOf = dupResult.ExistingID
				mediaItem.DeletedAt = &now
				messages = append(messages, "Duplicate detected: "+uniqueFilename+" ("+string(dupResult.Type)+")")
			}
		}

		if err := h.store.SaveMedia(mediaItem); err != nil {
			os.Remove(targetPath)
			messages = append(messages, "Failed to save to database "+uniqueFilename+": "+err.Error())
			errors++
			continue
		}

		mediaIDs = append(mediaIDs, mediaItem.ID)
		uploaded++

		if mediaItem.DeletedAt == nil {
			h.thumbService.QueueAllThumbnails(mediaItem.ID)
		}

		messages = append(messages, "Uploaded: "+uniqueFilename)
	}

	if uploaded > 0 {
		h.cache.Clear()
	}

	h.jsonResponse(w, map[string]interface{}{
		"uploaded":  uploaded,
		"errors":    errors,
		"media_ids": mediaIDs,
		"messages":  messages,
	})
}
