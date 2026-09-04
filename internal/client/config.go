package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/justin06lee/grokbox/internal/proto"
)

// Profile is a room this machine has joined before, remembered so the next
// command does not need the invite code again.
type Profile struct {
	Server string    `json:"server"`
	Room   string    `json:"room"`
	Key    string    `json:"key"`
	Name   string    `json:"name"`
	Token  string    `json:"token,omitempty"` // session, reused until it expires
	Seq    int64     `json:"seq,omitempty"`   // last message this machine read
	Used   time.Time `json:"used"`
}

// Invite rebuilds the invite code for a profile.
func (p Profile) Invite() proto.Invite {
	return proto.Invite{Server: p.Server, Room: p.Room, Key: p.Key}
}

// Config is the on-disk client state.
type Config struct {
	Profiles []Profile `json:"profiles"`
}

// UserDir is where grokbox keeps client state.
func UserDir() string {
	if d := os.Getenv("GROKBOX_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "grokbox")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".grokbox"
	}
	return filepath.Join(home, ".config", "grokbox")
}

// ConfigPath is the client state file. It holds room keys, so it is written
// with owner-only permissions.
func ConfigPath() string { return filepath.Join(UserDir(), "client.json") }

// LoadConfig reads saved profiles. A missing file is not an error.
func LoadConfig() (*Config, error) {
	b, err := os.ReadFile(ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return &Config{}, nil // a corrupt cache is not worth failing over
	}
	return &c, nil
}

// Save writes the config back.
func (c *Config) Save() error {
	if err := os.MkdirAll(UserDir(), 0o700); err != nil {
		return err
	}
	sort.Slice(c.Profiles, func(i, j int) bool { return c.Profiles[i].Used.After(c.Profiles[j].Used) })
	if len(c.Profiles) > 20 {
		c.Profiles = c.Profiles[:20]
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := ConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ConfigPath())
}

// Remember records a successful join, replacing any earlier entry for the
// same server and room.
func (c *Config) Remember(p Profile) {
	p.Used = time.Now().UTC()
	p.Server = strings.TrimRight(p.Server, "/")
	for i, old := range c.Profiles {
		if old.Server == p.Server && old.Room == p.Room {
			c.Profiles[i] = p
			return
		}
	}
	c.Profiles = append(c.Profiles, p)
}

// Latest returns the most recently used profile, optionally restricted to one
// room. It returns nil when nothing matches.
func (c *Config) Latest(room string) *Profile {
	sort.Slice(c.Profiles, func(i, j int) bool { return c.Profiles[i].Used.After(c.Profiles[j].Used) })
	for i := range c.Profiles {
		if room == "" || c.Profiles[i].Room == room {
			return &c.Profiles[i]
		}
	}
	return nil
}
