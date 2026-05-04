package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

var (
	InfoLog   *log.Logger
	ErrorLog  *log.Logger
	infoFile  *os.File
	errorFile *os.File
)

const (
	defaultDirPerm   = os.FileMode(0755)
	defaultFilePerm  = os.FileMode(0644)
	infoLogFileName  = "info.log"
	errorLogFileName = "error.log"
	logFlags         = log.LstdFlags | log.Lshortfile
)

// Init инициализирует логгеры для записи только в файлы
func Init(logsPath string) error {
	// Создать директорию для логов
	if err := os.MkdirAll(logsPath, defaultDirPerm); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Открыть info.log (append mode)
	infoPath := filepath.Join(logsPath, infoLogFileName)
	var err error
	infoFile, err = os.OpenFile(infoPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, defaultFilePerm)
	if err != nil {
		return fmt.Errorf("failed to create info.log: %w", err)
	}

	// Открыть error.log (append mode)
	errorPath := filepath.Join(logsPath, errorLogFileName)
	errorFile, err = os.OpenFile(errorPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, defaultFilePerm)
	if err != nil {
		infoFile.Close()
		return fmt.Errorf("failed to create error.log: %w", err)
	}

	// Настроить логгеры (ТОЛЬКО запись в файлы, БЕЗ stdout)
	InfoLog = log.New(infoFile, "", logFlags)
	ErrorLog = log.New(errorFile, "", logFlags)

	// Первая запись в лог для подтверждения что логирование работает
	InfoLog.Printf("Logger initialized successfully. Logs directory: %s", logsPath)
	InfoLog.Printf("Info log file: %s", infoPath)
	InfoLog.Printf("Error log file: %s", errorPath)

	return nil
}

// Cleanup закрывает файлы логов
func Cleanup() error {
	var errInfo, errError error

	if infoFile != nil {
		infoFile.Sync()
		errInfo = infoFile.Close()
	}

	if errorFile != nil {
		errorFile.Sync()
		errError = errorFile.Close()
	}

	if errInfo != nil {
		return errInfo
	}
	return errError
}
