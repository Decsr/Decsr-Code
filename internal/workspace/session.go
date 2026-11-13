package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxTranscript caps the transcript of a session; the oldest lines are dropped.
const maxTranscript = 500

// maxSessionBytes caps one session file that is read.
const maxSessionBytes = 8 << 20

var sessionIDRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// Line is one transcript entry. Who is "you", "lead", "system" or "error".
type Line struct {
	Who  string    `json:"who"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Session is one conversation in one project: transcript, running summary and
// one graph. It is stored in <project>/.decsr/sessions/<id>.json.
type Session struct {
	ID         string    `json:"id"`    // time-sortable: 20261113-104512-<4 hex>
	Title      string    `json:"title"` // first task's graph title; "" until then
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Transcript []Line    `json:"transcript,omitempty"` // capped at 500 lines (oldest dropped)
	Summary    string    `json:"summary,omitempty"`    // compacted history
	NextID     int       `json:"next_id,omitempty"`    // next T number; never reused
	Graph      *Graph    `json:"graph,omitempty"`
	In         int       `json:"input_tokens,omitempty"`  // lead usage for the session
	Out        int       `json:"output_tokens,omitempty"` // lead usage for the session
}

// SessionInfo is the listing entry of a saved session.
type SessionInfo struct {
	ID, Title                   string
	UpdatedAt                   time.Time
	Todo, Running, Done, Failed int
	Path                        string
}

var (
	idMu    sync.Mutex
	usedIDs = map[string]bool{}
)

// NewSession returns an empty session with a fresh ID. IDs are unique within
// the process even when created in the same second.
func NewSession() *Session {
	now := time.Now()
	idMu.Lock()
	defer idMu.Unlock()
	var id string
	for {
		var b [2]byte
		_, _ = rand.Read(b[:])
		id = now.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
		if !usedIDs[id] {
			break
		}
	}
	usedIDs[id] = true
	return &Session{ID: id, CreatedAt: now, UpdatedAt: now}
}

// SessionsDir is <projectRoot>/.decsr/sessions.
func SessionsDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".decsr", "sessions")
}

func sessionPath(projectRoot, id string) (string, error) {
	if !sessionIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	return filepath.Join(SessionsDir(projectRoot), id+".json"), nil
}

// AddLine appends a transcript line, dropping the oldest beyond 500.
func (s *Session) AddLine(who, text string) {
	s.Transcript = append(s.Transcript, Line{Who: Sanitize(who), Text: SanitizeText(text), At: time.Now()})
	if over := len(s.Transcript) - maxTranscript; over > 0 {
		s.Transcript = append([]Line(nil), s.Transcript[over:]...)
	}
}

// Save writes the session atomically with mode 0o600 and sets UpdatedAt.
func (s *Session) Save(projectRoot string) error {
	p, err := sessionPath(projectRoot, s.ID)
	if err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p, data, ".session-*.tmp")
}

// LoadSession reads one session of the project. The graph gets the same checks
// as Load; nodes saved as running come back as todo.
func LoadSession(projectRoot, id string) (*Session, error) {
	p, err := sessionPath(projectRoot, id)
	if err != nil {
		return nil, err
	}
	data, err := readCapped(p, maxSessionBytes)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("load %s: %w", p, err)
	}
	if s.ID != id {
		return nil, fmt.Errorf("load %s: session id %q does not match the file", p, s.ID)
	}
	if s.Graph != nil {
		if err := s.Graph.prepare(p); err != nil {
			return nil, err
		}
	}
	if over := len(s.Transcript) - maxTranscript; over > 0 {
		s.Transcript = s.Transcript[over:]
	}
	s.Title = Sanitize(s.Title)
	for i := range s.Transcript {
		s.Transcript[i].Who = Sanitize(s.Transcript[i].Who)
		s.Transcript[i].Text = SanitizeText(s.Transcript[i].Text)
	}
	return &s, nil
}

// readCapped reads a file of at most max bytes; a bigger one is an error.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- validated session path under the project root
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d MB", path, max>>20)
	}
	return data, nil
}

// CountSessions is the number of saved sessions of the project: the *.json
// files in the sessions directory whose name is a valid session ID. Files are
// not read.
func CountSessions(projectRoot string) int {
	entries, err := os.ReadDir(SessionsDir(projectRoot))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".json") && sessionIDRe.MatchString(strings.TrimSuffix(name, ".json")) {
			n++
		}
	}
	return n
}

// ListSessions returns the project's sessions, newest first. Unreadable or
// corrupt or oversized (over 8 MB) files and non-session files are skipped.
func ListSessions(projectRoot string) []SessionInfo {
	entries, err := os.ReadDir(SessionsDir(projectRoot))
	if err != nil {
		return nil
	}
	var out []SessionInfo
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		s, err := LoadSession(projectRoot, id)
		if err != nil {
			continue
		}
		info := SessionInfo{ID: s.ID, Title: s.Title, UpdatedAt: s.UpdatedAt, Path: filepath.Join(SessionsDir(projectRoot), name)}
		if s.Graph != nil {
			info.Todo, info.Running, info.Done, info.Failed = s.Graph.Counts()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// DeleteSession removes a saved session.
func DeleteSession(projectRoot, id string) error {
	p, err := sessionPath(projectRoot, id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// MigrateLegacy wraps the old .decsr/workspace.json in a session, saves it and
// renames the old file to workspace.json.migrated. It returns (nil, nil) when
// there is no old file or the project already has sessions.
func MigrateLegacy(projectRoot string) (*Session, error) {
	legacy := filepath.Join(projectRoot, ".decsr", FileName)
	if _, err := os.Stat(legacy); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if entries, err := os.ReadDir(SessionsDir(projectRoot)); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				return nil, nil
			}
		}
	}
	g, err := Load(legacy)
	if err != nil {
		return nil, err
	}
	s := NewSession()
	s.Title = g.Title
	s.Graph = g
	s.NextID = g.nextWorkNumber()
	if err := s.Save(projectRoot); err != nil {
		return nil, err
	}
	if err := os.Rename(legacy, legacy+".migrated"); err != nil {
		return s, fmt.Errorf("session saved but could not rename %s: %w", legacy, err)
	}
	return s, nil
}

// nextWorkNumber is one more than the highest T<number> ID in the graph (1 for
// none).
func (g *Graph) nextWorkNumber() int {
	max := 0
	for _, n := range g.Nodes {
		if len(n.ID) > 1 && n.ID[0] == 'T' {
			if v, err := strconv.Atoi(n.ID[1:]); err == nil && v > max {
				max = v
			}
		}
	}
	return max + 1
}
