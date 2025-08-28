package logger

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// LogEntry represents a single log entry
type LogEntry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}

// Logger is a SQLite-backed logger
type Logger struct {
	db         *sql.DB
	stdLogger  *log.Logger
	dbPath     string
	mutex      sync.Mutex
	initialized bool
}

var instance *Logger
var once sync.Once

// GetLogger returns the singleton logger instance
func GetLogger() *Logger {
	once.Do(func() {
		instance = &Logger{
			stdLogger: log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile),
		}
	})
	return instance
}

// Initialize initializes the logger with the given database path
func (l *Logger) Initialize(dbPath string) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.initialized {
		return nil
	}

	// Convert to absolute path if it's relative
	if !filepath.IsAbs(dbPath) {
		absPath, err := filepath.Abs(dbPath)
		if err != nil {
			l.stdLogger.Printf("[WARN] Failed to get absolute path for database: %v", err)
		} else {
			dbPath = absPath
			l.stdLogger.Printf("[INFO] Using absolute database path: %s", dbPath)
		}
	}

	// Ensure the directory exists with more robust error handling
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		l.stdLogger.Printf("[ERROR] Failed to create log directory %s: %v", dir, err)
		// Try to use a fallback directory in the current working directory
		dbPath = "mcproxy_logs.db"
		l.stdLogger.Printf("[WARN] Using fallback database path: %s", dbPath)
	}

	// Check if directory was actually created
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		l.stdLogger.Printf("[ERROR] Log directory %s does not exist after creation attempt", dir)
		// Try to use a fallback directory in the current working directory
		dbPath = "mcproxy_logs.db"
		l.stdLogger.Printf("[WARN] Using fallback database path: %s", dbPath)
	}

	// Try to open the database with different methods if needed
	var db *sql.DB
	var err error

	// First attempt: Use a DSN with pragmas for better reliability
	dsn := fmt.Sprintf("file:%s?_journal=WAL&_timeout=5000", dbPath)
	db, err = sql.Open("sqlite", dsn)

	// If that fails, try a simpler approach
	if err != nil {
		l.stdLogger.Printf("[WARN] Failed to open database with DSN: %v", err)
		db, err = sql.Open("sqlite", dbPath)
		if err != nil {
			l.stdLogger.Printf("[ERROR] Failed to open database with simple path: %v", err)
			// Try in-memory database as a last resort
			db, err = sql.Open("sqlite", ":memory:")
			if err != nil {
				return fmt.Errorf("all database open attempts failed: %w", err)
			}
			l.stdLogger.Printf("[WARN] Using in-memory database as fallback")
		}
	}

	// Set pragmas for better performance and reliability
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA cache_size = 1000;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA temp_store = MEMORY;",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			l.stdLogger.Printf("[WARN] Failed to set pragma (%s): %v", pragma, err)
			// Continue anyway, these are optimizations
		}
	}

	// Create the logs table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL,
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			source TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
		CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
		CREATE INDEX IF NOT EXISTS idx_logs_level_timestamp ON logs(level, timestamp);
	`)
	if err != nil {
		l.stdLogger.Printf("[ERROR] Failed to create logs table: %v", err)
		db.Close()

		// Try in-memory database as a last resort
		db, err = sql.Open("sqlite", ":memory:")
		if err != nil {
			return fmt.Errorf("failed to open in-memory database: %w", err)
		}

		// Create the logs table in memory
		_, err = db.Exec(`
			CREATE TABLE IF NOT EXISTS logs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				timestamp DATETIME NOT NULL,
				level TEXT NOT NULL,
				message TEXT NOT NULL,
				source TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
			CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
			CREATE INDEX IF NOT EXISTS idx_logs_level_timestamp ON logs(level, timestamp);
		`)
		if err != nil {
			db.Close()
			return fmt.Errorf("failed to create in-memory logs table: %w", err)
		}

		l.stdLogger.Printf("[WARN] Using in-memory database as fallback")
	}

	// Test the database with a simple query
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master").Scan(&count)
	if err != nil {
		l.stdLogger.Printf("[WARN] Database test query failed: %v", err)
		// Continue anyway, the database might still be usable
	}

	l.db = db
	l.dbPath = dbPath
	l.initialized = true

	// Log initialization message directly to avoid deadlock
	// (Info method would try to acquire the mutex that's already held)
	l.stdLogger.Printf("[INFO] Logger initialized with SQLite database at %s", dbPath)
	return nil
}

// Close closes the logger database connection
func (l *Logger) Close() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.db != nil {
		return l.db.Close()
	}
	return nil
}

// log logs a message with the given level
func (l *Logger) log(level LogLevel, calldepth int, format string, v ...interface{}) {
	// Format the message
	msg := fmt.Sprintf(format, v...)

	// Log to stdout
	l.stdLogger.Output(calldepth+1, fmt.Sprintf("[%s] %s", level.String(), msg))

	// Log to database if initialized
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if !l.initialized || l.db == nil {
		return
	}

	// Get the source file and line
	_, file, line, ok := runtime.Caller(calldepth)
	if !ok {
		file = "unknown"
		line = 0
	}
	source := fmt.Sprintf("%s:%d", filepath.Base(file), line)

	// Insert into database with retry logic
	maxRetries := 3
	var err error

	for i := 0; i < maxRetries; i++ {
		// Try to insert the log entry
		_, err = l.db.Exec(
			"INSERT INTO logs (timestamp, level, message, source) VALUES (?, ?, ?, ?)",
			time.Now().UTC(), level.String(), msg, source,
		)

		if err == nil {
			// Success, no need to retry
			break
		}

		// If this is not the last retry, wait a bit before trying again
		if i < maxRetries-1 {
			l.stdLogger.Printf("[WARN] Failed to write log to database (attempt %d/%d): %v", 
				i+1, maxRetries, err)
			time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
		}
	}

	if err != nil {
		// All retries failed
		l.stdLogger.Printf("[ERROR] Failed to write log to database after %d attempts: %v", 
			maxRetries, err)

		// Check if we need to reinitialize the database connection
		if isConnectionError(err) {
			l.tryReconnect()
		}
	} else {
		// Ensure the log is immediately written to disk
		_, err = l.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		if err != nil {
			l.stdLogger.Printf("[WARN] Failed to checkpoint WAL: %v", err)
		}
	}
}

// isConnectionError checks if an error is related to database connection issues
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()
	connectionErrors := []string{
		"database is locked",
		"disk I/O error",
		"disk full",
		"out of memory",
		"unable to open database file",
		"no such table",
		"database disk image is malformed",
	}

	for _, errText := range connectionErrors {
		if strings.Contains(errMsg, errText) {
			return true
		}
	}

	return false
}

// tryReconnect attempts to reconnect to the database
func (l *Logger) tryReconnect() {
	l.stdLogger.Printf("[WARN] Database connection error detected, attempting to reconnect")

	// Close the current connection
	if l.db != nil {
		l.db.Close()
	}

	// Try to reopen the database
	db, err := sql.Open("sqlite", l.dbPath)
	if err != nil {
		l.stdLogger.Printf("[ERROR] Failed to reopen database: %v", err)
		l.db = nil
		return
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		l.stdLogger.Printf("[ERROR] Failed to ping reopened database: %v", err)
		db.Close()
		l.db = nil
		return
	}

	// Update the database connection
	l.db = db
	l.stdLogger.Printf("[INFO] Successfully reconnected to database")
}

// Debug logs a debug message
func (l *Logger) Debug(format string, v ...interface{}) {
	l.log(DEBUG, 2, format, v...)
}

// Info logs an info message
func (l *Logger) Info(format string, v ...interface{}) {
	l.log(INFO, 2, format, v...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, v ...interface{}) {
	l.log(WARN, 2, format, v...)
}

// Error logs an error message
func (l *Logger) Error(format string, v ...interface{}) {
	l.log(ERROR, 2, format, v...)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(format string, v ...interface{}) {
	l.log(FATAL, 2, format, v...)
	os.Exit(1)
}

// GetLogs retrieves logs from the database with optional filtering
func (l *Logger) GetLogs(limit, offset int, level string, startTime, endTime time.Time) ([]LogEntry, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if !l.initialized || l.db == nil {
		// Return empty logs instead of error to avoid breaking the UI
		l.stdLogger.Printf("[WARN] GetLogs called but logger not initialized")
		return []LogEntry{}, nil
	}

	// Build the query
	query := "SELECT id, timestamp, level, message, source FROM logs WHERE 1=1"
	args := []interface{}{}

	// Add filters
	if level != "" {
		query += " AND level = ?"
		args = append(args, level)
	}

	if !startTime.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, startTime.UTC())
	}

	if !endTime.IsZero() {
		query += " AND timestamp <= ?"
		args = append(args, endTime.UTC())
	}

	// Add ordering and limits
	query += " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	// Execute the query with retry logic
	var rows *sql.Rows
	var err error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		rows, err = l.db.Query(query, args...)
		if err == nil {
			break
		}

		// If this is not the last retry, wait a bit before trying again
		if i < maxRetries-1 {
			l.stdLogger.Printf("[WARN] Failed to query logs (attempt %d/%d): %v", 
				i+1, maxRetries, err)
			time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
		}
	}

	if err != nil {
		// All retries failed
		l.stdLogger.Printf("[ERROR] Failed to query logs after %d attempts: %v", maxRetries, err)

		// Check if we need to reinitialize the database connection
		if isConnectionError(err) {
			l.tryReconnect()
		}

		// Return empty logs instead of error to avoid breaking the UI
		return []LogEntry{}, nil
	}
	defer rows.Close()

	// Parse the results
	logs := []LogEntry{}
	for rows.Next() {
		var entry LogEntry
		var timestamp string
		err := rows.Scan(&entry.ID, &timestamp, &entry.Level, &entry.Message, &entry.Source)
		if err != nil {
			l.stdLogger.Printf("[ERROR] Failed to scan log entry: %v", err)
			continue // Skip this entry but continue processing others
		}

		// Parse the timestamp - try multiple formats
		formats := []string{
			time.RFC3339Nano,                    // ISO 8601 with nanoseconds (e.g., "2025-08-27T20:37:20.055403996Z")
			"2006-01-02T15:04:05.999999999Z07:00", // ISO 8601 with nanoseconds and explicit timezone
			"2006-01-02 15:04:05.999999999Z07:00", // Space separator with nanoseconds
			"2006-01-02 15:04:05.999999999-07:00", // Original format
		}

		parsed := false
		for _, format := range formats {
			entry.Timestamp, err = time.Parse(format, timestamp)
			if err == nil {
				parsed = true
				break
			}
		}

		if !parsed {
			l.stdLogger.Printf("[WARN] Failed to parse timestamp '%s' with any known format", timestamp)
			entry.Timestamp = time.Now() // Fallback to current time if parsing fails
		}

		logs = append(logs, entry)
	}

	if err := rows.Err(); err != nil {
		l.stdLogger.Printf("[ERROR] Error iterating log rows: %v", err)
		// Continue anyway, return what we have
	}

	return logs, nil
}

// GetRecentLogs returns the most recent logs, optionally filtered by level
func (l *Logger) GetRecentLogs(limit int, level string, since time.Time) ([]LogEntry, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if !l.initialized || l.db == nil {
		// Return empty logs instead of error to avoid breaking the UI
		l.stdLogger.Printf("[WARN] GetRecentLogs called but logger not initialized")
		return []LogEntry{}, nil
	}

	// Build the query
	query := "SELECT id, timestamp, level, message, source FROM logs WHERE 1=1"
	args := []interface{}{}

	// Add filters
	if level != "" {
		query += " AND level = ?"
		args = append(args, level)
	}

	if !since.IsZero() {
		query += " AND timestamp > ?"
		args = append(args, since.UTC())
	}

	// Add ordering and limits
	query += " ORDER BY timestamp DESC LIMIT ?"
	args = append(args, limit)

	// Execute the query with retry logic
	var rows *sql.Rows
	var err error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		rows, err = l.db.Query(query, args...)
		if err == nil {
			break
		}

		// If this is not the last retry, wait a bit before trying again
		if i < maxRetries-1 {
			l.stdLogger.Printf("[WARN] Failed to query recent logs (attempt %d/%d): %v", 
				i+1, maxRetries, err)
			time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
		}
	}

	if err != nil {
		// All retries failed
		l.stdLogger.Printf("[ERROR] Failed to query recent logs after %d attempts: %v", maxRetries, err)

		// Check if we need to reinitialize the database connection
		if isConnectionError(err) {
			l.tryReconnect()
		}

		// Return empty logs instead of error to avoid breaking the UI
		return []LogEntry{}, nil
	}
	defer rows.Close()

	// Parse the results
	logs := []LogEntry{}
	for rows.Next() {
		var entry LogEntry
		var timestamp string
		err := rows.Scan(&entry.ID, &timestamp, &entry.Level, &entry.Message, &entry.Source)
		if err != nil {
			l.stdLogger.Printf("[ERROR] Failed to scan log entry: %v", err)
			continue // Skip this entry but continue processing others
		}

		// Parse the timestamp - try multiple formats
		formats := []string{
			time.RFC3339Nano,                    // ISO 8601 with nanoseconds (e.g., "2025-08-27T20:37:20.055403996Z")
			"2006-01-02T15:04:05.999999999Z07:00", // ISO 8601 with nanoseconds and explicit timezone
			"2006-01-02 15:04:05.999999999Z07:00", // Space separator with nanoseconds
			"2006-01-02 15:04:05.999999999-07:00", // Original format
		}

		parsed := false
		for _, format := range formats {
			entry.Timestamp, err = time.Parse(format, timestamp)
			if err == nil {
				parsed = true
				break
			}
		}

		if !parsed {
			l.stdLogger.Printf("[WARN] Failed to parse timestamp '%s' with any known format", timestamp)
			entry.Timestamp = time.Now() // Fallback to current time if parsing fails
		}

		logs = append(logs, entry)
	}

	if err := rows.Err(); err != nil {
		l.stdLogger.Printf("[ERROR] Error iterating log rows: %v", err)
		// Continue anyway, return what we have
	}

	return logs, nil
}

// GetLogCount returns the total number of logs matching the given filters
func (l *Logger) GetLogCount(level string, startTime, endTime time.Time) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if !l.initialized || l.db == nil {
		// Return 0 instead of error to avoid breaking the UI
		l.stdLogger.Printf("[WARN] GetLogCount called but logger not initialized")
		return 0, nil
	}

	// Build the query
	query := "SELECT COUNT(*) FROM logs WHERE 1=1"
	args := []interface{}{}

	// Add filters
	if level != "" {
		query += " AND level = ?"
		args = append(args, level)
	}

	if !startTime.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, startTime.UTC())
	}

	if !endTime.IsZero() {
		query += " AND timestamp <= ?"
		args = append(args, endTime.UTC())
	}

	// Execute the query with retry logic
	var count int
	var err error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		err = l.db.QueryRow(query, args...).Scan(&count)
		if err == nil {
			break
		}

		// If this is not the last retry, wait a bit before trying again
		if i < maxRetries-1 {
			l.stdLogger.Printf("[WARN] Failed to count logs (attempt %d/%d): %v", 
				i+1, maxRetries, err)
			time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
		}
	}

	if err != nil {
		// All retries failed
		l.stdLogger.Printf("[ERROR] Failed to count logs after %d attempts: %v", maxRetries, err)

		// Check if we need to reinitialize the database connection
		if isConnectionError(err) {
			l.tryReconnect()
		}

		// Return 0 instead of error to avoid breaking the UI
		return 0, nil
	}

	return count, nil
}
