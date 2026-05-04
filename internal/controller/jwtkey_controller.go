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
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/agent-control-plane/aip-k8s/internal/jwt"
)

const (
	jwtKeySecretName   = "aip-jwt-signing-key"
	jwtKeySecretKey    = "tls.key"
	jwtKeySecretCert   = "tls.crt"
	defaultRotationTTL = 90 * 24 * time.Hour // 90 days
	requeueInterval    = 1 * time.Hour       // check every hour
)

// JWTKeyReconciler ensures the aip-jwt-signing-key Secret exists with a valid
// Ed25519 key pair, rotating it when the key exceeds RotationTTL.
type JWTKeyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Namespace   string
	RotationTTL time.Duration
	Clock       func() time.Time
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,resourceNames=aip-jwt-signing-key,verbs=get;list;watch;create;update;patch

func (r *JWTKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	now := r.now()

	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Name: jwtKeySecretName, Namespace: r.Namespace}, &secret)

	if err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Failed to get JWT signing key Secret")
		return ctrl.Result{}, err
	}

	needsRotation := false
	if errors.IsNotFound(err) {
		logger.Info("JWT signing key Secret not found, creating")
		needsRotation = true
	} else if r.isStale(&secret, now) {
		logger.Info("JWT signing key is stale, rotating",
			"created", secret.CreationTimestamp.Time,
			"age", now.Sub(secret.CreationTimestamp.Time))
		needsRotation = true
	}

	if needsRotation {
		if err := r.rotateKey(ctx); err != nil {
			logger.Error(err, "Failed to rotate JWT signing key")
			return ctrl.Result{}, err
		}
		logger.Info("JWT signing key rotated successfully")
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

func (r *JWTKeyReconciler) isStale(secret *corev1.Secret, now time.Time) bool {
	age := now.Sub(secret.CreationTimestamp.Time)
	return age >= r.ttl()
}

func (r *JWTKeyReconciler) rotateKey(ctx context.Context) error {
	privatePEM, publicPEM, err := jwt.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generate key pair: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jwtKeySecretName,
			Namespace: r.Namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			jwtKeySecretKey:  privatePEM,
			jwtKeySecretCert: publicPEM,
		},
	}

	var existing corev1.Secret
	if err := r.Get(ctx, client.ObjectKeyFromObject(secret), &existing); err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, secret); err != nil {
				return fmt.Errorf("create secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("get existing secret: %w", err)
	}

	existing.Data = secret.Data
	if err := r.Update(ctx, &existing); err != nil {
		return fmt.Errorf("update secret: %w", err)
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Named("jwtkey").
		Complete(r)
}
