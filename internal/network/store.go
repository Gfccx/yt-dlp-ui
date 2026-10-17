package network

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

type Update struct {
	Settings
	KeepPassword bool `json:"keep_password"`
}

type View struct {
	Settings
	PasswordSet bool   `json:"password_set"`
	Effective   string `json:"effective"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Settings
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, errors.New("cannot read network settings file")
	}
	if json.Unmarshal(data, &s.cfg) != nil {
		return nil, errors.New("invalid network settings file")
	}
	cfg, err := normalize(s.cfg)
	if err != nil {
		return nil, err
	}
	s.cfg = cfg
	return s, nil
}

func normalize(cfg Settings) (Settings, error) {
	if cfg.URL != "" || cfg.Enabled {
		u, err := ParseURL(cfg.URL)
		if err != nil {
			return Settings{}, err
		}
		cfg.URL = u.String()
	}
	return cfg, nil
}

func (s *Store) Current() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) View() View {
	cfg := s.Current()
	v := View{Settings: cfg, Effective: "Direct"}
	if cfg.Enabled {
		v.Effective = Label(cfg.URL)
	}
	if u, err := ParseURL(cfg.URL); err == nil && u.User != nil {
		_, v.PasswordSet = u.User.Password()
		u.User = url.User(u.User.Username())
		v.URL = u.String()
	}
	return v
}

// prepare is called under the store lock, including for unsaved tests.
func (s *Store) prepare(update Update) (Settings, error) {
	cfg, err := normalize(update.Settings)
	if err != nil || !update.KeepPassword {
		return cfg, err
	}
	old, oldErr := ParseURL(s.cfg.URL)
	next, nextErr := ParseURL(cfg.URL)
	if oldErr != nil || nextErr != nil || old.User == nil || next.User == nil || old.Scheme != next.Scheme || old.Host != next.Host || old.User.Username() != next.User.Username() {
		return Settings{}, errors.New("re-enter the password after changing proxy address, protocol or username")
	}
	if _, present := next.User.Password(); present {
		return Settings{}, errors.New("keep_password cannot be combined with a new password")
	}
	next.User = old.User
	cfg.URL = next.String()
	return cfg, nil
}

func (s *Store) Preview(update Update) (Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prepare(update)
}

// Save commits to disk atomically before publishing the new in-memory setting.
func (s *Store) Save(update Update) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.prepare(update)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return errors.New("cannot create network settings directory")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".network-*")
	if err != nil {
		return errors.New("cannot create network settings file")
	}
	defer os.Remove(f.Name())
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("cannot write network settings file")
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return errors.New("cannot replace network settings file")
	}
	s.cfg = cfg
	return nil
}
