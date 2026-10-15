// Package store keeps surveys and their responses in a directory: each
// survey is a JSON file, and its responses are lines of JSON in a second
// file, so everything can be read, backed up and diffed with ordinary tools.
package store

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

var (
	// ErrNotFound reports a survey that does not exist (or a name that
	// could not be one).
	ErrNotFound = errors.New("no such survey")
	// ErrExists reports a survey name that is taken.
	ErrExists = errors.New("a survey with that name already exists")
)

// maxResponse bounds one stored response; a larger one is refused.
const maxResponse = 64 << 10

var validSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// Valid reports whether name can be a survey's name. Names become file
// names, so anything else, such as "../x", is not one.
func Valid(name string) bool { return validSlug.MatchString(name) }

// Store is a directory of surveys. It is safe for concurrent use.
type Store struct {
	surveys, responses string
	mu                 sync.Mutex
}

// Open returns the store in dir, creating the directory if needed.
func Open(dir string) (*Store, error) {
	s := &Store{surveys: filepath.Join(dir, "surveys"), responses: filepath.Join(dir, "responses")}
	for _, d := range []string{s.surveys, s.responses} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Entry describes a survey in a listing.
type Entry struct {
	Name      string
	Title     string
	Responses int
}

// List returns the surveys by name.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := os.ReadDir(s.surveys)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, f := range files {
		name, ok := strings.CutSuffix(f.Name(), ".json")
		if !ok || !Valid(name) {
			continue
		}
		def, err := os.ReadFile(filepath.Join(s.surveys, f.Name()))
		if err != nil {
			return nil, err
		}
		var head struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(def, &head) // a damaged file is listed by its name
		n, err := s.count(name)
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Name: name, Title: head.Title, Responses: n})
	}
	return out, nil
}

func (s *Store) count(name string) (int, error) {
	b, err := os.ReadFile(s.responseFile(name))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return bytes.Count(b, []byte("\n")), err
}

func (s *Store) surveyFile(name string) string   { return filepath.Join(s.surveys, name+".json") }
func (s *Store) responseFile(name string) string { return filepath.Join(s.responses, name+".jsonl") }

// Get returns a survey's definition.
func (s *Store) Get(name string) (jsontext.Value, error) {
	if !Valid(name) {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.surveyFile(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

// Create stores a new survey, failing with ErrExists if the name is taken.
func (s *Store) Create(name string, def jsontext.Value) error {
	if !Valid(name) {
		return fmt.Errorf("%q is not a usable survey name", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.surveyFile(name)); err == nil {
		return ErrExists
	}
	return s.write(name, def)
}

// Update replaces a survey's definition. Responses are kept.
func (s *Store) Update(name string, def jsontext.Value) error {
	if !Valid(name) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.surveyFile(name)); err != nil {
		return ErrNotFound
	}
	return s.write(name, def)
}

// write replaces the file in one step, so a reader never sees half of it.
func (s *Store) write(name string, def jsontext.Value) error {
	pretty := slices.Clone(def)
	if err := pretty.Indent(jsontext.WithIndent("  ")); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.surveys, ".new-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(pretty, '\n'))
	if err = errors.Join(err, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), s.surveyFile(name)); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

// Delete removes a survey and its responses.
func (s *Store) Delete(name string) error {
	if !Valid(name) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.surveyFile(name)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if err := os.Remove(s.responseFile(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// AddResponse records one response, a JSON object.
func (s *Store) AddResponse(name string, v jsontext.Value) error {
	if !Valid(name) {
		return ErrNotFound
	}
	line := slices.Clone(v)
	if err := line.Compact(); err != nil {
		return err
	}
	if len(line) > maxResponse {
		return fmt.Errorf("a response may be at most %d bytes", maxResponse)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.surveyFile(name)); err != nil {
		return ErrNotFound
	}
	f, err := os.OpenFile(s.responseFile(name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return errors.Join(err, f.Close())
}

// Responses returns a survey's responses, oldest first.
func (s *Store) Responses(name string) ([]jsontext.Value, error) {
	if !Valid(name) {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.surveyFile(name)); err != nil {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.responseFile(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []jsontext.Value
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, maxResponse+1)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			out = append(out, slices.Clone(jsontext.Value(line)))
		}
	}
	return out, errors.Join(sc.Err(), f.Close())
}
