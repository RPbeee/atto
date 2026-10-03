package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Config struct {
	Lint LintConfig `json:"lint"`
}
type LintConfig struct {
	OnSave         bool       `json:"on_save"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Rules          []LintRule `json:"rules"`
}
type LintRule struct {
	Pattern   string   `json:"pattern"`
	Command   []string `json:"command"`
	Directory string   `json:"directory,omitempty"`
}

func loadConfig(path string) (Config, error) {
	c := Config{}
	explicit := path != ""
	if !explicit {
		dir, err := os.UserConfigDir()
		if err != nil {
			return c, err
		}
		path = filepath.Join(dir, "atto", "config.json")
	}
	f, err := os.Open(path)
	if !explicit && errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("config %s: expected one JSON object", path)
	}
	if c.Lint.TimeoutSeconds == 0 {
		c.Lint.TimeoutSeconds = 10
	}
	if c.Lint.TimeoutSeconds < 1 || c.Lint.TimeoutSeconds > 300 {
		return c, errors.New("lint.timeout_seconds must be 1..300")
	}
	for i, r := range c.Lint.Rules {
		if r.Pattern == "" {
			return c, fmt.Errorf("lint rule %d: empty pattern", i+1)
		}
		if _, err := filepath.Match(r.Pattern, ""); err != nil {
			return c, fmt.Errorf("lint rule %d: %w", i+1, err)
		}
		if len(r.Command) == 0 || r.Command[0] == "" {
			return c, fmt.Errorf("lint rule %d: empty command", i+1)
		}
	}
	return c, nil
}

func (c LintConfig) rule(path string) (LintRule, bool) {
	for _, r := range c.Rules {
		if ok, _ := filepath.Match(r.Pattern, filepath.Base(path)); ok {
			return r, true
		}
	}
	return LintRule{}, false
}
