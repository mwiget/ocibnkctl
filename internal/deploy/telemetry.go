package deploy

import (
	"context"
	"fmt"
	"io"
	"time"
)

const (
	// TelemetryNamespace is where the tmm pod (and so the webhook) lives.
	TelemetryNamespace = "default"
	// Locally-built images imported into the k3s nodes (no registry behind them).
	telemetryExporterImage = "tmm-stat-exporter:dev"
	telemetryWebhookImage  = "tmm-stat-webhook:dev"
	// DefaultRemoteWriteURL is bnk-forge's Prometheus on the host docker-bridge
	// gateway — reachable from the tmm pod by plain egress (outbound isn't
	// TMM-hooked). bnk-forge runs that Prometheus host-networked with
	// --web.enable-remote-write-receiver. Override via poc.yaml
	// telemetry.remote_write_url.
	DefaultRemoteWriteURL = "http://172.17.0.1:9491/api/v1/write"
)

// TelemetryInputs parameterize the webhook manifest template.
type TelemetryInputs struct {
	Namespace      string
	ClusterName    string
	ExporterImage  string
	WebhookImage   string
	RemoteWriteURL string
}

// RenderTelemetryWebhook renders the webhook + cert manifests, filling defaults.
func RenderTelemetryWebhook(in TelemetryInputs) (string, error) {
	if in.Namespace == "" {
		in.Namespace = TelemetryNamespace
	}
	if in.ExporterImage == "" {
		in.ExporterImage = telemetryExporterImage
	}
	if in.WebhookImage == "" {
		in.WebhookImage = telemetryWebhookImage
	}
	if in.RemoteWriteURL == "" {
		in.RemoteWriteURL = DefaultRemoteWriteURL
	}
	return renderTemplate("templates/telemetry-webhook.yaml.tmpl", in)
}

// DeployTelemetry installs the mutating webhook that injects the tmm-stat-exporter
// sidecar into the operator-managed f5-tmm pod, then rolls the tmm pod so the
// sidecar is injected. The serving cert is issued by cert-manager (deploy
// prereqs) and the caBundle is filled by cainjector. clusterName is used to
// import the locally-built images into the k3s nodes (best-effort); rwURL
// overrides the remote_write target (empty = DefaultRemoteWriteURL).
func DeployTelemetry(ctx context.Context, r *Runner, out io.Writer, clusterName, rwURL string) error {
	if clusterName != "" {
		// Local k3s, no registry behind these images: if they aren't built, the
		// injected sidecar would ImagePullBackOff (and a NotReady sidecar makes
		// the whole tmm pod NotReady). Skip cleanly instead — so this is safe to
		// run unconditionally in the e2e chain.
		if !localImagePresent(ctx, telemetryExporterImage) || !localImagePresent(ctx, telemetryWebhookImage) {
			fmt.Fprintln(out, "telemetry images not built (run `make telemetry-images`) — skipping telemetry install.")
			return nil
		}
		fmt.Fprintln(out, "[1/4] Importing exporter + webhook images into the cluster runtime ...")
		if err := LoadImage(ctx, clusterName, telemetryExporterImage, out); err != nil {
			return fmt.Errorf("load exporter image: %w", err)
		}
		if err := LoadImage(ctx, clusterName, telemetryWebhookImage, out); err != nil {
			return fmt.Errorf("load webhook image: %w", err)
		}
	}

	fmt.Fprintln(out, "[2/4] Applying the telemetry webhook (cert-manager cert + MutatingWebhookConfiguration) ...")
	manifest, err := RenderTelemetryWebhook(TelemetryInputs{ClusterName: clusterName, RemoteWriteURL: rwURL})
	if err != nil {
		return err
	}
	if err := r.Apply(ctx, manifest); err != nil {
		return fmt.Errorf("apply telemetry webhook: %w", err)
	}

	fmt.Fprintln(out, "[3/4] Waiting for the webhook to be Available + caBundle injected ...")
	if err := r.Wait(ctx, TelemetryNamespace, "Available", "deployment/tmm-stat-webhook", 3*time.Minute); err != nil {
		return fmt.Errorf("tmm-stat-webhook not Available: %w", err)
	}
	// cainjector patches the MutatingWebhookConfiguration's caBundle from the
	// cert-manager Certificate. Wait for it before rolling tmm, else the first
	// admission call fails TLS (failurePolicy=Ignore) and the sidecar is missed.
	if err := waitCABundle(ctx, r, "tmm-stat-webhook", time.Minute); err != nil {
		return err
	}

	fmt.Fprintln(out, "[4/4] Rolling the tmm pod so the exporter sidecar is injected ...")
	if err := r.Kubectl(ctx, "-n", TelemetryNamespace, "delete", "pod", "-l", "app=f5-tmm", "--wait=false"); err != nil {
		// A missing tmm pod (telemetry deployed before tmm) is fine — the next
		// pod the operator creates goes through the webhook.
		fmt.Fprintf(out, "      | no tmm pod to roll yet (%v) — it will be injected on creation\n", err)
	}
	fmt.Fprintln(out, "      tmm-stat-exporter will push tmstat metrics to "+effectiveURL(rwURL)+".")
	return nil
}

// waitCABundle polls until cainjector has populated the MutatingWebhookConfiguration's
// caBundle (non-empty), so admission TLS works on the first try.
func waitCABundle(ctx context.Context, r *Runner, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ca, _ := r.KubectlCapture(ctx, "get", "mutatingwebhookconfiguration", name,
			"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}")
		if len(ca) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cainjector did not populate %s caBundle within %s (is cert-manager-cainjector running?)", name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func effectiveURL(rwURL string) string {
	if rwURL == "" {
		return DefaultRemoteWriteURL
	}
	return rwURL
}
