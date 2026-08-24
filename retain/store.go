package retain

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const flushInterval = 10 * time.Millisecond

// Store keeps last values in memory and persists them to SQLite in WAL mode.
type Store struct {
	db *sql.DB

	mu     sync.RWMutex
	values map[string][]byte
	dirty  map[string][]byte

	stopFlush chan struct{}
	flushDone chan struct{}
	closeOnce sync.Once
}

// Open loads the SQLite file into memory and starts background flushes.
func Open(path string) (*Store, error) {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create retain database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open retain database: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA synchronous=NORMAL`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure retain database: %w", err)
		}
	}

	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS last_value (
			subject TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create retain table: %w", err)
	}

	store := &Store{
		db:        db,
		values:    make(map[string][]byte),
		dirty:     make(map[string][]byte),
		stopFlush: make(chan struct{}),
		flushDone: make(chan struct{}),
	}
	if err := store.load(); err != nil {
		_ = db.Close()
		return nil, err
	}
	go store.flushLoop()
	return store, nil
}

// Put records the latest payload for subject. The write is visible to Get
// immediately and is flushed to SQLite in the background.
func (store *Store) Put(subject string, payload []byte) {
	if store == nil || subject == "" {
		return
	}
	copied := append([]byte(nil), payload...)
	store.mu.Lock()
	store.values[subject] = copied
	store.dirty[subject] = copied
	store.mu.Unlock()
}

// Get returns a copy of the latest payload.
func (store *Store) Get(subject string) ([]byte, bool) {
	if store == nil {
		return nil, false
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	payload, ok := store.values[subject]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), payload...), true
}

// Subjects returns a snapshot of full NATS subjects currently retained.
func (store *Store) Subjects() []string {
	if store == nil {
		return nil
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	subjects := make([]string, 0, len(store.values))
	for subject := range store.values {
		subjects = append(subjects, subject)
	}
	return subjects
}

// Close flushes pending writes and closes the database.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	var err error
	store.closeOnce.Do(func() {
		close(store.stopFlush)
		<-store.flushDone
		err = store.db.Close()
	})
	return err
}

func (store *Store) load() error {
	rows, err := store.db.Query(`SELECT subject, payload FROM last_value`)
	if err != nil {
		return fmt.Errorf("load retain values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var subject string
		var payload []byte
		if err := rows.Scan(&subject, &payload); err != nil {
			return fmt.Errorf("scan retain value: %w", err)
		}
		store.values[subject] = append([]byte(nil), payload...)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load retain values: %w", err)
	}
	return nil
}

func (store *Store) flushLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	defer close(store.flushDone)
	for {
		select {
		case <-store.stopFlush:
			store.flushDirty()
			store.flushDirty()
			return
		case <-ticker.C:
			store.flushDirty()
		}
	}
}

func (store *Store) flushDirty() {
	store.mu.Lock()
	if len(store.dirty) == 0 {
		store.mu.Unlock()
		return
	}
	batch := store.dirty
	store.dirty = make(map[string][]byte)
	store.mu.Unlock()

	tx, err := store.db.Begin()
	if err != nil {
		store.requeue(batch)
		return
	}
	statement, err := tx.Prepare(
		`INSERT OR REPLACE INTO last_value(subject, payload, updated_at) VALUES (?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		store.requeue(batch)
		return
	}
	now := time.Now().UnixNano()
	for subject, payload := range batch {
		if _, err := statement.Exec(subject, payload, now); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			store.requeue(batch)
			return
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		store.requeue(batch)
		return
	}
	if err := tx.Commit(); err != nil {
		store.requeue(batch)
	}
}

func (store *Store) requeue(batch map[string][]byte) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for subject, payload := range batch {
		if _, exists := store.dirty[subject]; exists {
			continue
		}
		store.dirty[subject] = payload
	}
}
