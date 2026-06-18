package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// LoadImage imports a locally-built image from the host docker daemon into every
// k3s node container of the cluster (docker save → docker cp → `ctr -n k8s.io
// image import`). Used for images that have no registry behind them (the
// telemetry exporter/webhook are built locally); on a fresh cluster the kubelet
// would otherwise fall back to docker.io and ImagePullBackOff.
//
// Best-effort: a missing local image (assume it's registry-pullable) or no
// k3s-<cluster>-* node containers (remote cluster) are skipped with a note.
func LoadImage(ctx context.Context, clusterName, image string, out io.Writer) error {
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", image).Run(); err != nil {
		fmt.Fprintf(out, "      | image %s not in the local docker daemon — assuming registry-pullable (build it with `make exporter-image`/`webhook-image`)\n", image)
		return nil
	}
	psOut, err := exec.CommandContext(ctx, "docker", "ps",
		"--filter", "name=k3s-"+clusterName+"-", "--format", "{{.Names}}").Output()
	if err != nil {
		return fmt.Errorf("list k3s node containers: %w", err)
	}
	nodes := strings.Fields(strings.TrimSpace(string(psOut)))
	if len(nodes) == 0 {
		fmt.Fprintf(out, "      | no k3s-%s-* node containers found — skipping image import (remote cluster?)\n", clusterName)
		return nil
	}

	tar, err := os.CreateTemp("", "ocibnk-img-*.tar")
	if err != nil {
		return err
	}
	tar.Close()
	defer os.Remove(tar.Name())
	if err := runQuiet(ctx, "docker", "save", image, "-o", tar.Name()); err != nil {
		return fmt.Errorf("docker save %s: %w", image, err)
	}
	for _, n := range nodes {
		fmt.Fprintf(out, "      | importing %s into %s ...\n", image, n)
		if err := runQuiet(ctx, "docker", "cp", tar.Name(), n+":/tmp/ocibnk-img.tar"); err != nil {
			return fmt.Errorf("docker cp into %s: %w", n, err)
		}
		if err := runQuiet(ctx, "docker", "exec", n,
			"ctr", "-n", "k8s.io", "image", "import", "/tmp/ocibnk-img.tar"); err != nil {
			return fmt.Errorf("ctr image import on %s: %w", n, err)
		}
		_ = runQuiet(ctx, "docker", "exec", n, "rm", "-f", "/tmp/ocibnk-img.tar")
	}
	return nil
}

// localImagePresent reports whether image is in the host docker daemon.
func localImagePresent(ctx context.Context, image string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", image).Run() == nil
}

// runQuiet runs a command, surfacing stderr only on failure.
func runQuiet(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return nil
}
