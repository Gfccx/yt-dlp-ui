// Package network owns proxy validation, persistence and connectivity checks.
package network

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ParseURL accepts an explicit scheme, host and port, with no other URL parts.
// Errors deliberately never include the input, which may contain credentials.
func ParseURL(raw string) (*url.URL, error) {
	invalid := errors.New("invalid proxy URL: use scheme://[user:password@]host:port (IPv6 must be bracketed)")
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\\?#") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return nil, invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, invalid
	}
	switch u.Scheme {
	case "http", "https", "socks4", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy scheme must be http, https, socks4, socks5 or socks5h")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return nil, invalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strings.Trim(port, "0123456789") != "" {
		return nil, errors.New("proxy port must be between 1 and 65535")
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 || strings.Contains(u.Host, "[") {
			return nil, invalid
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, invalid
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return nil, invalid
				}
			}
		}
	}
	if strings.Count(raw, "@") > 1 {
		return nil, invalid
	}
	if u.User != nil {
		user := u.User.Username()
		password, hasPassword := u.User.Password()
		if user == "" || strings.IndexFunc(user+password, unicode.IsControl) >= 0 {
			return nil, errors.New("proxy username is required for authentication; control characters are not allowed")
		}
		if u.Scheme == "socks4" && hasPassword && password != "" {
			return nil, errors.New("SOCKS4 supports a user ID only, not password authentication")
		}
		if (u.Scheme == "socks5" || u.Scheme == "socks5h") && (len(user) > 255 || len(password) > 255) {
			return nil, errors.New("SOCKS5 username and password must each be at most 255 bytes")
		}
	}
	u.Host = net.JoinHostPort(strings.ToLower(host), strconv.Itoa(n))
	return u, nil
}

// CLIProxy accounts for yt-dlp's SOCKS unquote_plus userinfo decoding.
func CLIProxy(raw string) string {
	u, err := ParseURL(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return u.Scheme + "://" + strings.ReplaceAll(u.User.String(), "+", "%2B") + "@" + u.Host
}

// Label contains no authentication information and is safe for jobs and SSE.
func Label(raw string) string {
	if raw == "" {
		return "Direct"
	}
	u, err := ParseURL(raw)
	if err != nil {
		return "Invalid proxy"
	}
	u.User = nil
	return u.String()
}

// Redact removes both encoded and decoded credentials from subprocess errors.
func Redact(message, raw string) string {
	u, err := ParseURL(raw)
	if err != nil || u.User == nil {
		return message
	}
	message = strings.ReplaceAll(message, raw, Label(raw))
	message = strings.ReplaceAll(message, CLIProxy(raw), Label(raw))
	message = strings.ReplaceAll(message, u.User.String(), "[redacted]")
	if _, encoded, ok := strings.Cut(u.User.String(), ":"); ok && encoded != "" {
		message = strings.ReplaceAll(message, strings.ReplaceAll(encoded, "+", "%2B"), "[redacted]")
		message = strings.ReplaceAll(message, encoded, "[redacted]")
	}
	if password, _ := u.User.Password(); password != "" {
		message = strings.ReplaceAll(message, password, "[redacted]")
	}
	message = strings.ReplaceAll(message, strings.ReplaceAll(url.User(u.User.Username()).String(), "+", "%2B"), "[redacted]")
	message = strings.ReplaceAll(message, url.User(u.User.Username()).String(), "[redacted]")
	return strings.ReplaceAll(message, u.User.Username(), "[redacted]")
}

type Policy struct {
	Mode string `json:"mode"` // global (default), direct, custom
	URL  string `json:"url,omitempty"`
}

// Selection is an immutable per-operation snapshot. URL must never be exposed.
type Selection struct {
	Mode string
	URL  string
}

func (s *Store) Select(p Policy) (Selection, error) {
	switch p.Mode {
	case "", "global":
		if p.URL != "" {
			return Selection{}, errors.New("proxy URL is only allowed with custom mode")
		}
		cfg := s.Current()
		raw := ""
		if cfg.Enabled {
			raw = cfg.URL
		}
		return Selection{Mode: "global", URL: raw}, nil
	case "direct":
		if p.URL != "" {
			return Selection{}, errors.New("direct mode cannot include a proxy URL")
		}
		return Selection{Mode: "direct"}, nil
	case "custom":
		u, err := ParseURL(p.URL)
		if err != nil {
			return Selection{}, err
		}
		return Selection{Mode: "custom", URL: u.String()}, nil
	default:
		return Selection{}, errors.New("proxy mode must be global, direct or custom")
	}
}
