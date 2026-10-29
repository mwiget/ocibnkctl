package deploy

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeFARTgz(t *testing.T, dockerConfig string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "far.tgz")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte(dockerConfig)
	hdr := &tar.Header{
		Name:     "f5-far-auth-key/.dockerconfigjson",
		Size:     int64(len(body)),
		Mode:     0o600,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return dst
}

func TestExtractFAR_DockerConfigFound(t *testing.T) {
	tgz := makeFARTgz(t, `{"auths":{"repo.f5.com":{"auth":"abc"}}}`)
	got, err := ExtractFARDockerConfig(tgz)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "repo.f5.com") {
		t.Errorf("payload = %s", string(got))
	}
}
