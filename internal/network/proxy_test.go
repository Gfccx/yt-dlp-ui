package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseURL(t *testing.T) {
	for _, raw := range []string{
		"http://192.168.1.100:7890", "https://proxy.example:443", "socks4://user@localhost:1080",
		"socks5://user:p%40%3A%2F%23%25@localhost:1080", "socks5h://[2001:db8::1]:1080",
		"http://u%3As%40er:p%3Ass%40word@[::1]:08080",
	} {
		t.Run(raw, func(t *testing.T) {
			u, err := ParseURL(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseURL(u.String()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{
		"", "localhost:1080", "ftp://localhost:21", "http://localhost", "http://:80",
		"http://localhost:0", "http://localhost:65536", "http://localhost:-1", "http://localhost:+80",
		"http://localhost:80/", "http://localhost:80/path", "http://localhost:80?", "http://localhost:80#",
		"http://localhost:80?secret=x", "http://localhost:80#secret", "http://a b:80",
		"http://2001:db8::1:8080", "http://[example.com]:80", "http://[fe80::1%25en0]:80",
		"http://u:p@ss@localhost:80", "http://u:%xx@localhost:80", "http://u:%0a@localhost:80",
		"http://:password@localhost:80", "http://u:p\\x@localhost:80", "http://localhost:80\n",
		"http://bad_host:80", "http://-host:80", "socks4://u:password@localhost:1080",
		"socks5://" + strings.Repeat("u", 256) + "@localhost:1080",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseURL(raw); err == nil {
				t.Fatal("accepted invalid URL")
			}
		})
	}
	u, err := ParseURL("http://u%3As%40er:p%3Ass%40word@[::1]:08080")
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.User.Username() != "u:s@er" || pw != "p:ss@word" || u.Host != "[::1]:8080" {
		t.Fatalf("wrong decoded URL: %s", u.Redacted())
	}
}

func TestStorePersistenceAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "network.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.View().Effective != "Direct" {
		t.Fatal("not direct by default")
	}
	raw := "http://user:p%40ss@localhost:7890"
	if err := s.Save(Update{Settings: Settings{Enabled: true, URL: raw}}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %o", st.Mode().Perm())
	}
	reloaded, err := Open(path)
	if err != nil || reloaded.Current().URL != raw {
		t.Fatalf("restart failed: %v", err)
	}
	view := s.View()
	data, _ := json.Marshal(view)
	if !view.PasswordSet || strings.Contains(string(data), "p%40ss") || strings.Contains(string(data), "p@ss") || view.Effective != "http://localhost:7890" {
		t.Fatalf("secret in view: %s", data)
	}
	if err := s.Save(Update{Settings: view.Settings, KeepPassword: true}); err != nil {
		t.Fatal(err)
	}
	if s.Current().URL != raw {
		t.Fatal("saved password lost")
	}
	if _, err := s.Preview(Update{Settings: Settings{Enabled: true, URL: "http://user@other-host:7890"}, KeepPassword: true}); err == nil {
		t.Fatal("forwarded saved password to another host")
	}
	if err := s.Save(Update{Settings: Settings{Enabled: false, URL: view.URL}, KeepPassword: true}); err != nil {
		t.Fatal(err)
	}
	if s.Current().Enabled || s.Current().URL != raw {
		t.Fatal("disable did not retain settings")
	}
	if err := s.Save(Update{Settings: Settings{Enabled: true, URL: "broken"}}); err == nil {
		t.Fatal("invalid update accepted")
	}
	if s.Current().Enabled {
		t.Fatal("invalid update modified settings")
	}
	if err := s.Save(Update{Settings: view.Settings}); err != nil {
		t.Fatal(err)
	}
	if s.View().PasswordSet {
		t.Fatal("password was not cleared")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("corrupt settings silently ignored")
	}
}

func TestFailedSaveDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "blocked", "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blocked"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Update{Settings: Settings{Enabled: true, URL: "http://localhost:80"}}); err == nil {
		t.Fatal("write should fail")
	}
	if s.Current().Enabled {
		t.Fatal("failed save changed live settings")
	}
}

func TestSelectionAndConcurrentAccess(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "network.json"))
	if err := s.Save(Update{Settings: Settings{Enabled: true, URL: "http://localhost:80"}}); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Select(Policy{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			for j := 0; j < 10; j++ {
				s.View()
				s.Select(Policy{})
				if err := s.Save(Update{Settings: Settings{Enabled: true, URL: "socks5h://localhost:1080"}}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	next, _ := s.Select(Policy{Mode: "global"})
	if old.URL != "http://localhost:80" || next.URL != "socks5h://localhost:1080" {
		t.Fatal("snapshot mutated")
	}
	direct, _ := s.Select(Policy{Mode: "direct"})
	if direct.URL != "" {
		t.Fatal("direct used global")
	}
	custom, err := s.Select(Policy{Mode: "custom", URL: "http://proxy:90"})
	if err != nil || custom.URL != "http://proxy:90" {
		t.Fatal("custom policy ignored")
	}
	for _, p := range []Policy{{Mode: "unknown"}, {Mode: "custom"}, {Mode: "direct", URL: "http://proxy:80"}, {Mode: "global", URL: "http://proxy:80"}} {
		if _, err := s.Select(p); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}

func TestRedact(t *testing.T) {
	raw := "http://private-user:p%40ss@localhost:80"
	got := Redact(raw+" private-user p@ss p%40ss", raw)
	if strings.Contains(got, "private-user") || strings.Contains(got, "p@ss") || strings.Contains(got, "p%40ss") {
		t.Fatal(got)
	}
}
