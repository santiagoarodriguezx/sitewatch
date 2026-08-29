package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DB           string        `yaml:"db" json:"db"`
	UserAgent    string        `yaml:"user_agent" json:"user_agent"`
	Timeout      time.Duration `yaml:"-" json:"timeout"`
	TimeoutText  string        `yaml:"timeout" json:"-"`
	MaxBody      int64         `yaml:"max_body" json:"max_body"`
	Retries      int           `yaml:"retries" json:"retries"`
	Concurrency  int           `yaml:"concurrency" json:"concurrency"`
	Rate         float64       `yaml:"rate" json:"rate"`
	MinScore     float64       `yaml:"min_score" json:"min_score"`
	AllowPrivate bool          `yaml:"allow_private" json:"allow_private"`
	Verbose      bool          `yaml:"verbose" json:"verbose"`
	Path         string        `yaml:"-" json:"config_file"`
}

func Load() (Config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, err
	}
	c := Config{DB: filepath.Join(dir, "sitewatch", "sitewatch.db"), UserAgent: "SiteWatch/0.1 (+https://github.com/sitewatch/sitewatch)", Timeout: 15 * time.Second, MaxBody: 10 << 20, Retries: 2, Concurrency: 10, Rate: 5, MinScore: .40, Path: filepath.Join(dir, "sitewatch", "config.yaml")}
	if b, err := os.ReadFile(c.Path); err == nil {
		if err := yaml.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("parse config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if c.TimeoutText != "" {
		c.Timeout, err = time.ParseDuration(c.TimeoutText)
		if err != nil {
			return c, fmt.Errorf("config timeout: %w", err)
		}
	}
	applyEnv(&c)
	return c, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("SITEWATCH_DB"); v != "" {
		c.DB = v
	}
	if v := os.Getenv("SITEWATCH_USER_AGENT"); v != "" {
		c.UserAgent = v
	}
	if v := os.Getenv("SITEWATCH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Timeout = d
		}
	}
	if v := os.Getenv("SITEWATCH_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Concurrency = n
		}
	}
	if v := os.Getenv("SITEWATCH_ALLOW_PRIVATE"); v != "" {
		c.AllowPrivate, _ = strconv.ParseBool(v)
	}
	if v := os.Getenv("SITEWATCH_VERBOSE"); v != "" {
		c.Verbose, _ = strconv.ParseBool(v)
	}
}
