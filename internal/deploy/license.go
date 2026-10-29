package deploy

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// ExtractFARDockerConfig opens the FAR tgz and returns the
// dockerconfigjson bytes ready to drop into a Secret's data field.
//
// Two on-disk formats are supported:
//
//  1. A literal `.dockerconfigjson` (older F5 packs).
//  2. A base64-encoded GCP service-account JSON (current F5 format —
//     filename is typically `cne_pull_64.json`). We decode it,
//     wrap it in the GAR `_json_key:<sa-json>` auth scheme, and build
//     the dockerconfigjson around `repo.f5.com`. Mirrors the recipe in
//     bnk-forge backend/routes/project_secrets.py.
func ExtractFARDockerConfig(tgzPath string) ([]byte, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return nil, fmt.Errorf("open far %s: %w", tgzPath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip %s: %w", tgzPath, err)
	}
	defer gz.Close()

	t := tar.NewReader(gz)
	for {
		hdr, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read %s: %w", tgzPath, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(t)
		if err != nil {
			return nil, err
		}
		base := hdr.Name
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}

		// Format 1: already a dockerconfigjson.
		if hasAuthsKey(body) {
			return body, nil
		}

		// Format 2: base64-encoded GCP SA JSON. Try base64-decode, then
		// require the decoded blob to be a service_account JSON.
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
		if err == nil && isServiceAccountJSON(decoded) {
			return buildGARDockerConfig(decoded), nil
		}
		// Or the file may already be raw service-account JSON (no base64 wrapper).
		if isServiceAccountJSON(body) {
			return buildGARDockerConfig(body), nil
		}

		return nil, fmt.Errorf("far tgz %s entry %s is neither a dockerconfigjson nor a (base64-encoded) GCP service_account JSON", tgzPath, base)
	}
	return nil, fmt.Errorf("far tgz %s: no regular files inside", tgzPath)
}

func hasAuthsKey(b []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m["auths"]
	return ok
}

func isServiceAccountJSON(b []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	t, _ := m["type"].(string)
	return t == "service_account"
}

// buildGARDockerConfig wraps a GCP service-account JSON as a
// dockerconfigjson for repo.f5.com (GAR-backed).
//
// Auth scheme: "_json_key_base64:<base64-of-sa-json>" base64-encoded.
// Matches f5-bnk-nvidia-bf3-installations v2.2.0-static/extra_playbooks/
// bnk.yml — which is what FLO's manifest-registry code path expects.
//
// (Google Artifact Registry also accepts "_json_key:<raw-sa-json>" for
// docker/helm CLI auth; but FLO's own auth parser only accepts the
// _json_key_base64 form. Bug-for-bug compatibility with f5-bnk wins.)
func buildGARDockerConfig(saJSON []byte) []byte {
	saB64 := base64.StdEncoding.EncodeToString(saJSON)
	auth := base64.StdEncoding.EncodeToString([]byte("_json_key_base64:" + saB64))
	cfg := fmt.Sprintf(`{"auths":{"repo.f5.com":{"auth":%q}}}`, auth)
	return []byte(cfg)
}

// UnwrapGARAuth is the inverse of buildGARDockerConfig: given a
// dockerconfigjson with auths.repo.f5.com.auth, returns the raw
// service-account JSON. Used by `helm registry login` which needs the
// password directly. Handles both auth forms in case an operator
// supplies a hand-crafted dockerconfigjson:
//
//	_json_key:<raw-json>            (older bnk-forge convention)
//	_json_key_base64:<base64-json>  (current f5-bnk convention)
func UnwrapGARAuth(dockerCfg []byte) (string, error) {
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(dockerCfg, &cfg); err != nil {
		return "", err
	}
	entry, ok := cfg.Auths["repo.f5.com"]
	if !ok {
		return "", fmt.Errorf("dockerconfigjson has no auths.repo.f5.com")
	}
	raw, err := base64.StdEncoding.DecodeString(entry.Auth)
	if err != nil {
		return "", fmt.Errorf("decode auth: %w", err)
	}
	s := string(raw)
	switch {
	case strings.HasPrefix(s, "_json_key_base64:"):
		// password is base64-encoded SA JSON; decode it back.
		pw := s[len("_json_key_base64:"):]
		dec, err := base64.StdEncoding.DecodeString(pw)
		if err != nil {
			return "", fmt.Errorf("decode _json_key_base64 password: %w", err)
		}
		return string(dec), nil
	case strings.HasPrefix(s, "_json_key:"):
		return s[len("_json_key:"):], nil
	default:
		return "", fmt.Errorf("auth does not start with _json_key: or _json_key_base64:")
	}
}

// FARCreds extracts the repo.f5.com upstream username/password from the FAR
// tgz, for placing in a client-side k3s registries.yaml `configs` block. This
// lets each cluster authenticate the pull-through cache itself (different BNK
// versions can carry different keys) while the cache stores no credential.
func FARCreds(tgzPath string) (username, password string, err error) {
	cfg, err := ExtractFARDockerConfig(tgzPath)
	if err != nil {
		return "", "", err
	}
	var doc struct {
		Auths map[string]struct {
			Auth     string `json:"auth"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return "", "", fmt.Errorf("parse far dockerconfig: %w", err)
	}
	e, ok := doc.Auths["repo.f5.com"]
	if !ok {
		for _, v := range doc.Auths { // fall back to any entry
			e = v
			ok = true
			break
		}
	}
	if !ok {
		return "", "", fmt.Errorf("far dockerconfig has no auth entries")
	}
	if e.Username != "" {
		return e.Username, e.Password, nil
	}
	dec, err := base64.StdEncoding.DecodeString(e.Auth)
	if err != nil {
		return "", "", fmt.Errorf("decode far auth: %w", err)
	}
	u, p, found := strings.Cut(string(dec), ":")
	if !found {
		return "", "", fmt.Errorf("far auth not in user:pass form")
	}
	return u, p, nil
}
