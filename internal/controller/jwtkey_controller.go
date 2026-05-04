/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/agent-control-plane/aip-k8s/internal/jwt"
)

const (
	jwtKeySecretName      = "aip-jwt-signing-key"
	jwtKeySecretKey       = "tls.key"
	jwtKeySecretCert      = "tls.crt"
	annotationLastRotated = "aip.io/jwt-key-rotated-at"
	defaultRotationTTL    = 90 * 24 * time.Hour
	requeueInterval       = 1 * time.Hour
)

// JWTKeyReconciler ensures the aip-jwt-signing-key Secret exists with a valid
// Ed25519 key pair, rotating it when the key exceeds RotationTTL.
type JWTKeyReconciler struct {
	client.Client
	APIReader   client.Reader
	Scheme      *runtime.Scheme
	Namespace   string
	RotationTTL time.Duration
	Clock       func() time.Time
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=create
// +kubebuilder:rbac:groups="",resources=secrets,resourceNames=aip-jwt-signing-key,verbs=get;update;patch

func (r *JWTKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	now := r.now()
	nn := types.NamespacedName{Name: jwtKeySecretName, Namespace: r.Namespace}

	var secret corev1.Secret
	err := r.APIReader.Get(ctx, nn, &secret)

	if err != nil && !errors.IsNotFound(err) {
		wrappedErr := fmt.Errorf("failed to get secret %s/%s: %w", nn.Namespace, nn.Name, err)
		logger.Error(wrappedErr, "Failed to get JWT signing key Secret")
		return ctrl.Result{}, wrappedErr
	}

	needsRotation := false
	if errors.IsNotFound(err) {
		logger.Info("JWT signing key Secret not found, creating")
		needsRotation = true
	} else if r.isStale(&secret, now) {
		logger.Info("JWT signing key is stale, rotating",
			"age", now.Sub(r.lastRotatedAt(&secret)))
		needsRotation = true
	}

	if needsRotation {
		if err := r.rotateKey(ctx, now); err != nil {
			wrappedErr := fmt.Errorf("failed to rotate secret %s/%s: %w", nn.Namespace, nn.Name, err)
			logger.Error(wrappedErr, "Failed to rotate JWT signing key")
			return ctrl.Result{}, wrappedErr
		}
		logger.Info("JWT signing key rotated successfully")
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

func (r *JWTKeyReconciler) lastRotatedAt(secret *corev1.Secret) time.Time {
	if secret.Annotations != nil {
		if raw, ok := secret.Annotations[annotationLastRotated]; ok {
			// Malformed annotation is intentionally ignored; fall back to
			// CreationTimestamp so the controller still rotates on schedule.
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				return t
			}
		}
	}
	return secret.CreationTimestamp.Time
}

func (r *JWTKeyReconciler) isStale(secret *corev1.Secret, now time.Time) bool {
	return now.Sub(r.lastRotatedAt(secret)) >= r.ttl()
}

func (r *JWTKeyReconciler) rotateKey(ctx context.Context, now time.Time) error {
	privatePEM, publicPEM, err := jwt.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generate key pair: %w", err)
	}

	newData := map[string][]byte{
		jwtKeySecretKey:  privatePEM,
		jwtKeySecretCert: publicPEM,
	}
	newAnnotations := map[string]string{
		annotationLastRotated: now.Format(time.RFC3339),
	}

	nn := types.NamespacedName{Name: jwtKeySecretName, Namespace: r.Namespace}
	var existing corev1.Secret
	if err := r.APIReader.Get(ctx, nn, &existing); err != nil {
		if errors.IsNotFound(err) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        jwtKeySecretName,
					Namespace:   r.Namespace,
					Annotations: newAnnotations,
				},
				Type: corev1.SecretTypeOpaque,
				Data: newData,
			}
			if err := r.Create(ctx, secret); err != nil {
				if errors.IsAlreadyExists(err) {
					return nil
				}
				return fmt.Errorf("create secret %s/%s: %w", nn.Namespace, nn.Name, err)
			}
			return nil
		}
		return fmt.Errorf("get existing secret %s/%s: %w", nn.Namespace, nn.Name, err)
	}

	base := existing.DeepCopy()
	existing.Data = newData
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations[annotationLastRotated] = now.Format(time.RFC3339)
	if err := r.Patch(ctx, &existing, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch secret %s/%s: %w", nn.Namespace, nn.Name, err)
	}
	return nil
}

func (r *JWTKeyReconciler) ttl() time.Duration {
	if r.RotationTTL > 0 {
		return r.RotationTTL
	}
	return defaultRotationTTL
}

func (r *JWTKeyReconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

// SetupWithManager sets up the controller with the Manager.
func (r *JWTKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Fire one startup reconcile so the controller creates the Secret immediately
	// on first install, then rely on periodic requeue (every hour) to check for
	// rotation. We avoid For(&corev1.Secret{}) to prevent cluster-wide Secret
	// informer creation; the narrow RBAC (resourceNames=aip-jwt-signing-key) is
	// incompatible with list/watch on all Secrets.
	startupCh := make(chan event.TypedGenericEvent[*corev1.Secret], 1)
	startupCh <- event.TypedGenericEvent[*corev1.Secret]{
		Object: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: jwtKeySecretName, Namespace: r.Namespace},
		},
	}
	close(startupCh)

	return ctrl.NewControllerManagedBy(mgr).
		Named("jwtkey").
		WatchesRawSource(source.Channel(startupCh, &handler.TypedEnqueueRequestForObject[*corev1.Secret]{})).
		Complete(r)
}
