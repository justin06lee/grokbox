package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/justin06lee/grokbox/internal/proto"
)

// store keeps room keys and transcripts on disk so an invite you handed out
// yesterday still works after a restart. It is optional: a server with no
// store simply forgets everything when it exits.
type store struct {
	dir string

	mu    sync.Mutex
	files map[string]*os.File
}

type roomRecord struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create store %s: %w", dir, err)
	}
	return &store{dir: dir, files: map[string]*os.File{}}, nil
}

// safeName guards against a room name that would escape the store directory.
// proto.CleanRoom already rejects separators; this is the belt to that braces.
func safeName(room string) bool {
	return room != "" && room != "." && room != ".." &&
		!strings.ContainsAny(room, `/\`) && !strings.HasPrefix(room, ".")
}

func (s *store) transcriptPath(room string) string {
	return filepath.Join(s.dir, room+".jsonl")
}

func (s *store) roomsPath() string { return filepath.Join(s.dir, "rooms.json") }

// loadRooms returns the rooms this store has seen, with their keys.
func (s *store) loadRooms() ([]roomRecord, error) {
	b, err := os.ReadFile(s.roomsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []roomRecord
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %w", s.roomsPath(), err)
	}
	return recs, nil
}

// saveRooms rewrites the room list atomically.
func (s *store) saveRooms(recs []roomRecord) error {
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.roomsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.roomsPath())
}

// append writes one message to the room's transcript. Failures are reported
// once and then ignored: losing the log must never take the chat down.
func (s *store) append(room string, m proto.Message) {
	if !safeName(room) {
		return
	}
	line, err := json.Marshal(m)
	if err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f := s.files[room]
	if f == nil {
		f, err = os.OpenFile(s.transcriptPath(room), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		s.files[room] = f
	}
	f.Write(append(line, '\n'))
}

// loadHistory returns the last n messages of a room's transcript.
func (s *store) loadHistory(room string, n int) []proto.Message {
	if !safeName(room) || n <= 0 {
		return nil
	}
	f, err := os.Open(s.transcriptPath(room))
	if err != nil {
		return nil
	}
	defer f.Close()

	ring := make([]proto.Message, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var m proto.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue // skip a torn line rather than refusing to start
		}
		if len(ring) == n {
			ring = append(ring[:0], ring[1:]...)
		}
		ring = append(ring, m)
	}
	return ring
}

func (s *store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.files {
		f.Close()
	}
	s.files = map[string]*os.File{}
	return nil
}
