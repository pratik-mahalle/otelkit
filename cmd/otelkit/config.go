package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// config is .otelkit.yaml: saved analyze settings, so real command lines stay short.
type config struct {
	Groups map[string][]string `yaml:"groups"` // group name → sources
	Names  map[string]string   `yaml:"names"`  // alias → source
	Vary   []string            `yaml:"vary"`
}

// loadConfig reads path; a missing default file is fine, a missing explicit one is an error.
// Relative file sources resolve from the config file's folder.
func loadConfig(path string, explicit bool) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return config{}, nil
		}
		return config{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c config
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return config{}, fmt.Errorf("%s: %w", path, err)
	}
	dir := filepath.Dir(path)
	resolve := func(s string) string {
		if strings.HasPrefix(s, "k8s://") || filepath.IsAbs(s) {
			return s
		}
		return filepath.Join(dir, s)
	}
	for g, srcs := range c.Groups {
		for i := range srcs {
			srcs[i] = resolve(srcs[i])
		}
		c.Groups[g] = srcs
	}
	for n, s := range c.Names {
		c.Names[n] = resolve(s)
	}
	return c, nil
}
