// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// k8s.go implements Kubernetes-based container management for tests.

package test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	errPodTerminal = errors.New("pod entered terminal phase")
	errPodTimeout  = errors.New("timeout waiting for pod to be running")
	errLdapTimeout = errors.New("timeout waiting for LDAP to be ready")
)

// IsRunningInKubernetes returns true when the process runs inside a
// Kubernetes pod (the kubelet always injects KUBERNETES_SERVICE_HOST).
func IsRunningInKubernetes() bool {
	_, exists := os.LookupEnv("KUBERNETES_SERVICE_HOST")

	return exists
}

func currentNamespace() string {
	ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "default"
	}

	return strings.TrimSpace(string(ns))
}

// SpinUpCarbonioLdapK8s creates a Kubernetes Pod running the Carbonio
// LDAP image and waits until it is ready. The returned LdapContainer
// exposes the pod IP and the fixed container port (1389).
//
// The pod is deleted when Stop() is called.
//
// Set the K8S_IMAGE_PULL_SECRET environment variable to the name of a
// Kubernetes docker-registry secret if the image requires authentication.
//
//nolint:thelper // not a helper, it's a setup function
func SpinUpCarbonioLdapK8s(t *testing.T, address, version string) (LdapContainer, context.Context) {
	lc, err := startLdapContainerK8s()
	if err != nil {
		t.Fatal(err)
	}

	return lc, context.Background()
}

func startLdapContainerK8s() (LdapContainer, error) {
	ctx := context.Background()

	config, err := rest.InClusterConfig()
	if err != nil {
		return LdapContainer{}, fmt.Errorf("in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return LdapContainer{}, fmt.Errorf("create k8s client: %w", err)
	}

	namespace := currentNamespace()
	podName := fmt.Sprintf("ldap-test-%d", time.Now().UnixNano())
	image := fmt.Sprintf(PublicImageAddress, LatestRelease)

	pod := newLdapPod(podName, namespace, image)

	if secret := os.Getenv("K8S_IMAGE_PULL_SECRET"); secret != "" {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: secret}}
	}

	_, err = clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return LdapContainer{}, fmt.Errorf("create pod: %w", err)
	}

	cleanup := func() {
		_ = clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	}

	if err := waitForPodRunning(ctx, clientset, namespace, podName); err != nil {
		cleanup()

		return LdapContainer{}, fmt.Errorf("pod failed: %w", err)
	}

	runningPod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		cleanup()

		return LdapContainer{}, fmt.Errorf("get pod: %w", err)
	}

	podIP := runningPod.Status.PodIP
	if podIP == "" {
		cleanup()

		return LdapContainer{}, fmt.Errorf("pod has no IP")
	}

	if err := waitForLdapReady(ctx, clientset, namespace, podName, podIP); err != nil {
		cleanup()

		return LdapContainer{}, fmt.Errorf("ldap not ready: %w", err)
	}

	lc := LdapContainer{
		Stop: cleanup,
		IP:   func() string { return podIP },
		Port: func() string { return "1389" },
	}
	lc.URL = func() string { return "ldap://" + podIP + ":1389" }

	return lc, nil
}

func newLdapPod(name, namespace, image string) *corev1.Pod {
	shmSize := resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": "ldap-test", "managed-by": "testcontainers"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "ldap",
				Image: image,
				Ports: []corev1.ContainerPort{{ContainerPort: 1389, Protocol: corev1.ProtocolTCP}},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "dshm", MountPath: "/dev/shm"},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "dshm",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium:    corev1.StorageMediumMemory,
						SizeLimit: shmSize,
					},
				},
			}},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
}

func waitForPodRunning(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	namespace, podName string,
) error {
	timeout := 5 * time.Minute
	interval := 2 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("%w: %s (%s)", errPodTerminal, podName, pod.Status.Phase)
		case corev1.PodPending, corev1.PodUnknown:
		}

		time.Sleep(interval)
	}

	return fmt.Errorf("%w: %s", errPodTimeout, podName)
}

func waitForLdapReady(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	namespace, podName, podIP string,
) error {
	timeout := 5 * time.Minute
	interval := 5 * time.Second
	deadline := time.Now().Add(timeout)
	expectedLog := `modifying entry "uid=zimbra,cn=admins,cn=zimbra"`
	addr := net.JoinHostPort(podIP, "1389")
	dialer := net.Dialer{Timeout: 2 * time.Second}

	logSeen := false
	consecutiveOK := 0
	requiredOK := 3

	for time.Now().Before(deadline) {
		if !logSeen {
			logs, err := getPodLogs(ctx, clientset, namespace, podName)
			if err == nil && strings.Contains(logs, expectedLog) {
				logSeen = true
			}
		}

		if logSeen {
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err == nil {
				_ = conn.Close()

				consecutiveOK++
				if consecutiveOK >= requiredOK {
					return nil
				}

				time.Sleep(2 * time.Second)

				continue
			}

			consecutiveOK = 0
		}

		time.Sleep(interval)
	}

	return fmt.Errorf("%w: %s", errLdapTimeout, podName)
}

func getPodLogs(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	namespace, podName string,
) (string, error) {
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{})

	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}

	defer func() { _ = stream.Close() }()

	buf := new(strings.Builder)

	_, err = io.Copy(buf, stream)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}
