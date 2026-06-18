// Command tmm-stat-webhook is a mutating admission webhook that injects the
// tmm-stat-exporter sidecar into f5-tmm pods at creation time. It exists for
// BNK deployments where the tmm pod is owned by the F5Tmm operator (so its spec
// can't be patched directly the way tmmlitectl patches its own static manifest):
// the webhook mutates the *actual pod* at admission, which survives operator
// reconciliation — the standard sidecar-injection pattern.
//
// It injects only into pods that carry the shared `f5tmstat` volume (the tmm
// pod) and aren't already injected, mounting that volume read-only and pointing
// the exporter at a Prometheus remote_write endpoint (push, because TMM hooks
// inbound TCP on its dataplane interfaces — see cmd/tmm-stat-exporter).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	sidecarName  = "tmm-stat-exporter"
	tmstatVolume = "f5tmstat"
	metricsPort  = 9099
	injectUserID = 65532
)

func main() {
	listen := flag.String("listen", ":8443", "HTTPS listen address")
	cert := flag.String("tls-cert", "/tls/tls.crt", "server certificate")
	key := flag.String("tls-key", "/tls/tls.key", "server private key")
	image := flag.String("exporter-image", "tmm-stat-exporter:dev", "exporter sidecar image")
	rwURL := flag.String("remote-write-url", "", "Prometheus remote_write URL the exporter pushes to")
	cluster := flag.String("cluster", "bnk", "cluster label added to every pushed series")
	flag.Parse()

	m := &mutator{image: *image, rwURL: *rwURL, cluster: *cluster}
	http.HandleFunc("/mutate", m.handle)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })

	log.Printf("tmm-stat-webhook: listening on %s, injecting %s (cluster=%s, remote_write=%s)", *listen, *image, *cluster, *rwURL)
	srv := &http.Server{Addr: *listen}
	log.Fatal(srv.ListenAndServeTLS(*cert, *key))
}

type mutator struct {
	image, rwURL, cluster string
}

type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

func (m *mutator) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil || review.Request == nil {
		http.Error(w, "bad AdmissionReview", http.StatusBadRequest)
		return
	}
	resp := &admissionv1.AdmissionResponse{UID: review.Request.UID, Allowed: true}

	var pod corev1.Pod
	if err := json.Unmarshal(review.Request.Object.Raw, &pod); err == nil {
		if patch := m.patchFor(&pod); patch != nil {
			pt := admissionv1.PatchTypeJSONPatch
			resp.Patch = patch
			resp.PatchType = &pt
		}
	}

	out := admissionv1.AdmissionReview{TypeMeta: review.TypeMeta, Response: resp}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// patchFor returns a JSONPatch adding the sidecar, or nil to leave the pod
// untouched (already injected, or not a tmm pod carrying the tmstat volume).
func (m *mutator) patchFor(pod *corev1.Pod) []byte {
	for _, c := range pod.Spec.Containers {
		if c.Name == sidecarName {
			return nil // already injected
		}
	}
	hasVol := false
	for _, v := range pod.Spec.Volumes {
		if v.Name == tmstatVolume {
			hasVol = true
			break
		}
	}
	if !hasVol {
		return nil // not a tmm pod (no shared tmstat segment to read)
	}

	patch, err := json.Marshal([]patchOp{{
		Op:    "add",
		Path:  "/spec/containers/-",
		Value: m.sidecar(),
	}})
	if err != nil {
		return nil
	}
	return patch
}

// sidecar mirrors the container tmmlitectl injects in setTMMScale: reads the
// shared tmstat volume RO and pushes Prometheus remote_write. cluster/pod/node
// ride along as labels via the downward API + $(VAR) expansion.
func (m *mutator) sidecar() corev1.Container {
	no := false
	yes := true
	uid := int64(injectUserID)
	return corev1.Container{
		Name:            sidecarName,
		Image:           m.image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Ports:           []corev1.ContainerPort{{Name: "metrics", ContainerPort: metricsPort}},
		Env: []corev1.EnvVar{
			{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
			{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
			{Name: "TMSTAT_REMOTE_WRITE_URL", Value: m.rwURL},
			{Name: "TMSTAT_EXTERNAL_LABELS", Value: fmt.Sprintf("cluster=%s,pod=$(POD_NAME),node=$(NODE_NAME)", m.cluster)},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                &uid,
			RunAsGroup:               &uid,
			RunAsNonRoot:             &yes,
			ReadOnlyRootFilesystem:   &yes,
			AllowPrivilegeEscalation: &no,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{"cpu": resource.MustParse("50m"), "memory": resource.MustParse("64Mi")},
			Limits:   corev1.ResourceList{"cpu": resource.MustParse("50m"), "memory": resource.MustParse("64Mi")},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: tmstatVolume, MountPath: "/var/tmstat", ReadOnly: true}},
	}
}
