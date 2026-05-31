package checkpoint

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Path   string    `json:"path,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

type Store struct {
	path string
	done map[string]Entry
}

func Open(path string) (*Store, error) {
	store := &Store{
		path: path,
		done: map[string]Entry{},
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		if entry.Status == "done" {
			store.done[entry.ID] = entry
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Done(id string) bool {
	_, ok := s.done[id]
	return ok
}

func (s *Store) Mark(entry Entry) error {
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(entry); err != nil {
		return err
	}
	if entry.Status == "done" {
		s.done[entry.ID] = entry
	}
	return nil
}
