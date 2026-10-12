//go:build e2e

package testcluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ManifestDeliveryFixture supplies an already-existing disposable namespace.
// The workflow under test, rather than the fixture, creates the application.
type ManifestDeliveryFixture struct {
	Config    *rest.Config
	Client    kubernetes.Interface
	Namespace string
}

func NewManifestDelivery(t *testing.T) ManifestDeliveryFixture {
	t.Helper()
	cfg := Config(t)
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-delivery-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := ns.UID
		_ = client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	})
	return ManifestDeliveryFixture{Config: cfg, Client: client, Namespace: ns.Name}
}

// RenderedApplication is a small, deliberately scaled-to-zero application. It
// tests API acceptance and reviewed delivery, not scheduling or readiness. The
// optional revision adds fields initially unowned by the create field manager;
// later revisions exercise updates owned by the existing no-force SSA manager.
func (f ManifestDeliveryFixture) RenderedApplication(revision string) []byte {
	data, annotation := "", ""
	if revision != "" {
		data = fmt.Sprintf("  reviewed: %q\n", revision)
		annotation = fmt.Sprintf("  annotations:\n    aster.dev/delivery-review: %q\n", revision)
	}
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: delivery-config
data:
  message: created-through-reviewed-delivery
%s---
apiVersion: v1
kind: Service
metadata:
  name: delivery-service
  namespace: %s
%sspec:
  selector:
    app: delivery-workload
  ports:
    - name: http
      port: 8080
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: delivery-workload
%sspec:
  replicas: 0
  selector:
    matchLabels:
      app: delivery-workload
  template:
    metadata:
      labels:
        app: delivery-workload
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: web
          image: %q
          command: [httpd, -f, -p, "8080", -h, /content]
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: content
              mountPath: /content
              readOnly: true
      volumes:
        - name: content
          configMap:
            name: delivery-config
`, data, f.Namespace, annotation, annotation, Image))
}
