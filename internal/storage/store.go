package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/photocore/photocore/internal/logger"
	bolt "go.etcd.io/bbolt"
)

const favoriteGlobalKey = "global"

var errFound = errors.New("found")

// Имена buckets
var (
	bucketMedia     = []byte("media")
	bucketUsers     = []byte("users")
	bucketSessions  = []byte("sessions")
	bucketAlbums    = []byte("albums")
	bucketTags      = []byte("tags")
	bucketIdxDir    = []byte("idx_dir")
	bucketIdxDate   = []byte("idx_date")
	bucketIdxTag    = []byte("idx_tag")
	bucketFavorites  = []byte("favorites")
	bucketUserFav    = []byte("userfav")
	bucketAPITokens  = []byte("api_tokens")
	bucketIdxChecksum = []byte("idx_checksum")
	bucketIdxHash    = []byte("idx_hash")
)

// FormatYearMonth форматирует дату как YYYY-MM для группировки timeline
func FormatYearMonth(t time.Time) string {
	return fmt.Sprintf("%04d-%02d", t.Year(), t.Month())
}

// LogShutdownSignal логирует получение сигнала завершения
func LogShutdownSignal(sig string) {
	logger.InfoLog.Printf("[DB] === SHUTDOWN SIGNAL RECEIVED: %s ===", sig)
}

// Store обертка над bbolt
type Store struct {
	db     *bolt.DB
	dbPath string
}

// NewStore создает новое хранилище
func NewStore(dbPath string) (*Store, error) {
	logger.InfoLog.Printf("[DB] === NewStore called ===")
	logger.InfoLog.Printf("[DB] DB path: %s", dbPath)
	logger.InfoLog.Printf("[DB] Go version: %s, OS: %s, Arch: %s", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, DefaultDirPerm); err != nil {
		logger.InfoLog.Printf("[DB] ERROR: failed to create db directory: %v", err)
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	if info, err := os.Stat(dbPath); err == nil {
		logger.InfoLog.Printf("[DB] Existing DB file: size=%d, mod=%s", info.Size(), info.ModTime().Format(time.RFC3339))
	} else if os.IsNotExist(err) {
		logger.InfoLog.Printf("[DB] DB file does not exist, will create")
	}

	logger.InfoLog.Printf("[DB] Opening bbolt database...")
	opts := &bolt.Options{
		Timeout:      5 * time.Second,
		NoSync:       false,
		FreelistType: bolt.FreelistMapType,
	}

	db, err := bolt.Open(dbPath, 0600, opts)
	if err != nil {
		logger.InfoLog.Printf("[DB] ERROR: Failed to open bbolt: %v", err)
		return nil, fmt.Errorf("failed to open bbolt: %w", err)
	}

	logger.InfoLog.Printf("[DB] SUCCESS: bbolt opened successfully")

	err = db.Update(func(tx *bolt.Tx) error {
		buckets := [][]byte{
			bucketMedia, bucketUsers, bucketSessions, bucketAlbums,
			bucketTags, bucketIdxDir, bucketIdxDate, bucketIdxTag,
			bucketFavorites, bucketUserFav, bucketAPITokens,
			bucketIdxChecksum, bucketIdxHash,
		}
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		logger.InfoLog.Printf("[DB] ERROR: Failed to create buckets: %v", err)
		return nil, err
	}

	logger.InfoLog.Printf("[DB] All buckets initialized")

	return &Store{db: db, dbPath: dbPath}, nil
}

// Close закрывает хранилище
func (s *Store) Close() error {
	logger.InfoLog.Printf("[DB] === Close called ===")
	logger.InfoLog.Printf("[DB] Syncing database...")
	if err := s.db.Sync(); err != nil {
		logger.InfoLog.Printf("[DB] WARNING: sync failed: %v", err)
	} else {
		logger.InfoLog.Printf("[DB] Sync successful")
	}

	logger.InfoLog.Printf("[DB] Closing bbolt...")
	err := s.db.Close()
	if err != nil {
		logger.InfoLog.Printf("[DB] ERROR: failed to close db: %v", err)
	} else {
		logger.InfoLog.Printf("[DB] SUCCESS: bbolt closed successfully")
	}

	logger.InfoLog.Printf("[DB] === Close completed ===")

	return err
}
