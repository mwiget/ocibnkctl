package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
)

const kitDir = "../../kit"

// envMap turns KEY=value lines into a map.
func envMap(lines []string) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

// shellDefaults sources kit/platform/platform.env in an empty environment
// and returns what it sets, as sh itself resolves it.
func shellDefaults(t *testing.T) map[string]string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `set -a; . "$1"; set +a; env`, "sh", filepath.Join(kitDir, "platform", "platform.env"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return envMap(strings.Split(string(out), "\n"))
}

// platform.env's defaults are what ocibnkctl passes for a default PoC, so a
// hand run and `ocibnkctl deploy` install the same thing. Paths and the
// helm/kit state locations are PoC-specific and skipped.
func TestPlatformEnvMatchesGo(t *testing.T) {
	defaults := shellDefaults(t)
	goEnv := envMap(platformEnv("/poc", poc.New("x"), "/poc/artifacts/kubeconfig"))
	goEnv["SHRINK_CPU"] = deploy.DefaultShrinkCPURequest
	goEnv["SHRINK_MEMORY"] = deploy.DefaultShrinkMemoryRequest
	skip := map[string]bool{"FAR_KEY": true, "JWT_FILE": true, "NODE_EXEC": true}
	for k, v := range goEnv {
		if skip[k] || strings.HasPrefix(k, "KIT_") || strings.HasPrefix(k, "HELM_") || k == "KUBECONFIG" {
			continue
		}
		d, ok := defaults[k]
		switch {
		case !ok:
			t.Errorf("ocibnkctl passes %s but kit/platform/platform.env has no default for it", k)
		case d != v:
			t.Errorf("%s: platform.env default %q, ocibnkctl passes %q", k, d, v)
		}
	}
}

// The BNK Forge runner image is Alpine: busybox sh, awk, base64, tar ...
// and no curl. Run the platform helpers that touch those with PATH holding
// nothing but busybox applets: the FAR credential must match Go's own
// extraction, the JWT must classify, and kit_fetch must download via wget.
func TestPlatformHelpersUnderBusybox(t *testing.T) {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("busybox not installed")
	}
	bin := t.TempDir()
	list, err := exec.Command(bb, "--list").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, applet := range strings.Fields(string(list)) {
		if err := os.Symlink(bb, filepath.Join(bin, applet)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "curl")); err == nil {
		t.Fatal("busybox provides curl here; the wget fallback would go untested")
	}

	work := t.TempDir()
	// A FAR tarball in the current format: a base64 GCP service-account key.
	sa := `{"type":"service_account","project_id":"stub","private_key":"k"}`
	if err := os.WriteFile(filepath.Join(work, "cne_pull_64.json"),
		[]byte(base64.StdEncoding.EncodeToString([]byte(sa))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	far := filepath.Join(work, "far.tgz")
	if out, err := exec.Command("tar", "-czf", far, "-C", work, "cne_pull_64.json").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, out)
	}
	enc := base64.RawURLEncoding.EncodeToString
	jwt := filepath.Join(work, "jwt")
	if err := os.WriteFile(jwt, []byte(enc([]byte(`{"jku":"https://product-tst.apis.f5networks.net/ee/v1/keys/jwks"}`))+
		"."+enc([]byte(`{"sub":"x"}`))+".sig\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte("fetched-ok\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(payload) }))
	defer srv.Close()

	lib, _ := filepath.Abs(filepath.Join(kitDir, "lib"))
	cmd := exec.Command(bb, "sh", "-c", `. "$KIT_LIB/common.sh"; . "$KIT_LIB/../platform/platform.env"; . "$KIT_LIB/../platform/lib.sh"
far_init; cat "$PLAT_WORK/dockerconfig.json"; echo; cat "$PLAT_WORK/registry-user"; echo
jwt_type
kit_fetch "$URL" "$SHA" "$OUT"; cat "$OUT"`)
	cmd.Env = []string{"PATH=" + bin, "HOME=" + work, "KIT_LIB=" + lib, "KIT_SCENARIO_DIR=" + lib,
		"FAR_KEY=" + far, "JWT_FILE=" + jwt, "URL=" + srv.URL + "/f",
		"SHA=" + fmt.Sprintf("%x", sha256.Sum256(payload)), "OUT=" + filepath.Join(work, "fetched")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("busybox run: %v\n%s", err, out)
	}
	want, err := deploy.ExtractFARDockerConfig(far)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 || lines[0] != string(want) || lines[1] != "_json_key" || lines[2] != "tst" || lines[3] != "fetched-ok" {
		t.Fatalf("under busybox got:\n%s\nwant dockerconfig %s, _json_key, tst, fetched-ok", out, want)
	}
}
