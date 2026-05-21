// Copyright 2026 dave-morin. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig creates a config.toml at a temp path with the given body and
// returns the file path. Also clears the env-var sources so tests start clean.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("SETLIST_FM_CONFIG", path)
	t.Setenv("SETLISTFM_API_KEY", "")
	t.Setenv("SETLIST_FM_API_KEY", "")
	return path
}

// runDoctor invokes the doctor command in JSON mode against the given config
// and returns the parsed report. Networked checks (api, credentials) run with
// the real http client; tests should ignore those keys.
func runDoctor(t *testing.T, configPath string, jsonMode bool) (map[string]any, string) {
	t.Helper()
	flags := &rootFlags{configPath: configPath, asJSON: jsonMode}
	cmd := newDoctorCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	_ = cmd.Execute()
	if !jsonMode {
		return nil, buf.String()
	}
	var report map[string]any
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\nbody=%s", err, buf.String())
	}
	return report, buf.String()
}

func TestDoctorEnvVarsOKWhenConfigProvidesAuth(t *testing.T) {
	path := writeConfig(t, `base_url = 'https://api.setlist.fm/rest'
fm_api_key = 'config-only-key'
`)
	report, _ := runDoctor(t, path, true)
	got, _ := report["env_vars"].(string)
	if !strings.HasPrefix(got, "OK config provides auth") {
		t.Fatalf("env_vars: got %q, want OK config provides auth ...", got)
	}
}

func TestDoctorEnvVarsFailWhenNoAuthAnywhere(t *testing.T) {
	path := writeConfig(t, `base_url = 'https://api.setlist.fm/rest'
fm_api_key = ''
`)
	report, _ := runDoctor(t, path, true)
	got, _ := report["env_vars"].(string)
	if !strings.HasPrefix(got, "ERROR missing required") {
		t.Fatalf("env_vars: got %q, want ERROR missing required ...", got)
	}
}

func TestDoctorEnvVarsOKWhenEnvVarSet(t *testing.T) {
	path := writeConfig(t, `base_url = 'https://api.setlist.fm/rest'
fm_api_key = ''
`)
	t.Setenv("SETLISTFM_API_KEY", "from-env")
	report, _ := runDoctor(t, path, true)
	got, _ := report["env_vars"].(string)
	if !strings.HasPrefix(got, "OK 1/1 available") {
		t.Fatalf("env_vars: got %q, want OK 1/1 available", got)
	}
}

func TestDoctorHintMentionsFreeAndAuthSetTokenAndEnvVar(t *testing.T) {
	path := writeConfig(t, `base_url = 'https://api.setlist.fm/rest'
fm_api_key = ''
`)
	report, _ := runDoctor(t, path, true)
	hint, _ := report["auth_hint"].(string)
	if !strings.Contains(strings.ToLower(hint), "free") {
		t.Errorf("auth_hint should mention 'free', got: %q", hint)
	}
	if !strings.Contains(hint, "setlist-fm-pp-cli auth set-token") {
		t.Errorf("auth_hint should mention auth set-token, got: %q", hint)
	}
	if !strings.Contains(hint, "SETLISTFM_API_KEY") {
		t.Errorf("auth_hint should mention SETLISTFM_API_KEY, got: %q", hint)
	}
	if !strings.Contains(hint, "https://www.setlist.fm/settings/api") {
		t.Errorf("auth_hint should link to settings/api, got: %q", hint)
	}
}

func TestDoctorHintOmittedWhenAuthConfigured(t *testing.T) {
	path := writeConfig(t, `base_url = 'https://api.setlist.fm/rest'
fm_api_key = 'configured-key'
`)
	report, _ := runDoctor(t, path, true)
	if _, ok := report["auth_hint"]; ok {
		t.Errorf("auth_hint should be omitted when auth is configured, report=%v", report)
	}
}

func TestDoctorHumanRenderingShowsHintAcrossMultipleLines(t *testing.T) {
	path := writeConfig(t, `base_url = 'https://api.setlist.fm/rest'
fm_api_key = ''
`)
	_, out := runDoctor(t, path, false)
	if !strings.Contains(out, "hint: Get a free API key") {
		t.Errorf("expected hint line to start with 'hint: Get a free API key', got:\n%s", out)
	}
	if !strings.Contains(out, "setlist-fm-pp-cli auth set-token") {
		t.Errorf("expected auth set-token line in rendered output, got:\n%s", out)
	}
}
