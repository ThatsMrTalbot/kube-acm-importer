/*
Copyright 2023 Adam Talbot.

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
	"crypto/x509"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/acm"
	"github.com/aws/aws-sdk-go/service/acm/acmiface"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/cert"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	acmv1alpha1 "github.com/thatsmrtalbot/kube-acm-importer/api/v1alpha1"
)

const ServiceAnnotation = "service.beta.kubernetes.io/aws-load-balancer-ssl-cert"
const Finalizer = "acm.kubespress.com/imported"
const FieldOwner = "acm.kubespress.com"

// ACMCertificateImportReconciler reconciles a ACMCertificateImport object
type ACMCertificateImportReconciler struct {
	client.Client
	ACM    acmiface.ACMAPI
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=acm.kubespress.com,resources=acmcertificateimports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=acm.kubespress.com,resources=acmcertificateimports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=acm.kubespress.com,resources=acmcertificateimports/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *ACMCertificateImportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Get the ACMCertificateImport object
	var certificateImport acmv1alpha1.ACMCertificateImport
	if err := r.Get(ctx, req.NamespacedName, &certificateImport); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if certificateImport.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, &certificateImport)
	}

	// Ensure the finalizer is set
	if err := r.ensureFinalizer(ctx, &certificateImport); err != nil {
		return ctrl.Result{}, err
	}

	// Ensure the certificate is up to date in ACM
	if err := r.ensureCertificateUpdated(ctx, &certificateImport); err != nil {
		return ctrl.Result{}, err
	}

	// Update the service annotations
	if err := r.ensureServiceAnnotations(ctx, &certificateImport); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ACMCertificateImportReconciler) ensureFinalizer(ctx context.Context, certificateImport *acmv1alpha1.ACMCertificateImport) error {
	// Add the finalizer, if this mutates the object update using the kubernetes client.
	if controllerutil.AddFinalizer(certificateImport, Finalizer) {
		return r.Update(ctx, certificateImport)
	}

	// No error
	return nil
}

func (r *ACMCertificateImportReconciler) ensureCertificateUpdated(ctx context.Context, certificateImport *acmv1alpha1.ACMCertificateImport) error {
	// Don't update the cert if we are frozen
	if pointer.BoolDeref(certificateImport.Spec.Frozen, false) {
		return nil
	}

	// Get desired cert from the secret
	certs, key, err := r.getCertificatesFromSecret(ctx, certificateImport)
	if err != nil {
		return err
	}

	// If the serial numbers match, do nothing
	if certificateImport.Status.SerialNumber == certs[0].SerialNumber.String() {
		return nil
	}

	// Encode the cert as a PEM
	certPem, err := cert.EncodeCertificates(certs[0])
	if err != nil {
		return err
	}

	// Encode the chain as a PEM
	chainPem, err := cert.EncodeCertificates(certs[1:]...)
	if err != nil {
		return err
	}

	// Input for the import
	input := acm.ImportCertificateInput{
		CertificateArn:   certificateImport.Status.ARN,
		Certificate:      certPem,
		CertificateChain: chainPem,
		PrivateKey:       key,
	}

	// Get the result
	log.FromContext(ctx).Info("importing certificate into acm", "arn", certificateImport.Status.ARN)
	output, err := r.ACM.ImportCertificate(&input)
	if err != nil {
		return err
	}

	// Update the object with the new ARN / Serial
	certificateImport.Status.ARN = output.CertificateArn
	certificateImport.Status.SerialNumber = certs[0].SerialNumber.String()
	if err := r.Status().Update(ctx, certificateImport); err != nil {
		return err
	}

	// Return no error
	return nil
}

func (r *ACMCertificateImportReconciler) ensureServiceAnnotations(ctx context.Context, certificateImport *acmv1alpha1.ACMCertificateImport) error {
	// Get the ARN as a string, if it is not set there is nothing to do
	arn := pointer.StringDeref(certificateImport.Status.ARN, "")
	if arn == "" {
		return nil
	}

	// Track errors
	var errors []error

	// Loop over references
	for _, serviceRef := range certificateImport.Spec.ServiceRefs {
		// Get the service
		var service corev1.Service
		if err := r.Get(ctx, client.ObjectKey{Namespace: certificateImport.Namespace, Name: serviceRef.Name}, &service); err != nil {
			errors = append(errors, fmt.Errorf("could not get service %q: %w", serviceRef.Name, err))
			continue
		}

		// If the annotation is already set, don't update
		if _, exists := service.Annotations[ServiceAnnotation]; exists {
			continue
		}

		// Use patch to set the annotation
		annoationKeyEscaped := ServiceAnnotation
		annoationKeyEscaped = strings.ReplaceAll(annoationKeyEscaped, "~", "~0")
		annoationKeyEscaped = strings.ReplaceAll(annoationKeyEscaped, "/", "~1")
		jsonPatch := fmt.Sprintf(`[{"op": "replace", "path": "/metadata/annotations/%s", "value": %q}]`, annoationKeyEscaped, arn)
		if err := r.Patch(ctx, &service, client.RawPatch(types.JSONPatchType, []byte(jsonPatch)), client.FieldOwner(FieldOwner)); err != nil {
			errors = append(errors, fmt.Errorf("could not patch service %q annotation: %w", serviceRef.Name, err))
		}
	}

	// Return aggregate errors
	return utilerrors.NewAggregate(errors)
}

func (r *ACMCertificateImportReconciler) reconcileDelete(ctx context.Context, certificateImport *acmv1alpha1.ACMCertificateImport) (ctrl.Result, error) {
	// Don't delete the cert if we are frozen or have no ARN to delete
	if pointer.BoolDeref(certificateImport.Spec.Frozen, false) || certificateImport.Status.ARN == nil {
		// Remove the finalizer
		if controllerutil.RemoveFinalizer(certificateImport, Finalizer) {
			return ctrl.Result{}, r.Update(ctx, certificateImport)
		}

		// No error, do nothing
		return ctrl.Result{}, nil
	}

	// Remove service annotations
	if err := r.removeServiceAnnotations(ctx, certificateImport); err != nil {
		return ctrl.Result{}, err
	}

	// Create API call input
	input := acm.DeleteCertificateInput{
		CertificateArn: certificateImport.Status.ARN,
	}

	// Perform DeleteCertificate API call
	_, err := r.ACM.DeleteCertificate(&input)

	// If the error is that the resource is not found, we can just continue
	var awsErr awserr.Error
	if errors.As(err, &awsErr) && awsErr.Code() == acm.ErrCodeResourceNotFoundException {
		err = nil
	}

	// Return all other errors
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update the object status, removing the ARN, this will trigger another reconcile that will remove the finalizer
	certificateImport.Status.ARN = nil
	return ctrl.Result{}, r.Status().Update(ctx, certificateImport)
}

func (r *ACMCertificateImportReconciler) removeServiceAnnotations(ctx context.Context, certificateImport *acmv1alpha1.ACMCertificateImport) error {
	// Get the ARN as a string, if it is not set there is nothing to do
	arn := pointer.StringDeref(certificateImport.Status.ARN, "")
	if arn == "" {
		return nil
	}

	// Track errors
	var errors []error

	// Loop over references
	for _, serviceRef := range certificateImport.Spec.ServiceRefs {
		// Get the service
		var service corev1.Service
		if err := r.Get(ctx, client.ObjectKey{Namespace: certificateImport.Namespace, Name: serviceRef.Name}, &service); err != nil {
			if !apierrors.IsNotFound(err) {
				errors = append(errors, fmt.Errorf("could not get service %q: %w", serviceRef.Name, err))
			}
			continue
		}

		// Only remove the annotation if its the one we manage
		if service.Annotations[ServiceAnnotation] != arn {
			continue
		}

		// Use patch to delete the annotation
		annoationKeyEscaped := ServiceAnnotation
		annoationKeyEscaped = strings.ReplaceAll(annoationKeyEscaped, "~", "~0")
		annoationKeyEscaped = strings.ReplaceAll(annoationKeyEscaped, "/", "~1")
		jsonPatch := fmt.Sprintf(`[{"op": "remove", "path": "/metadata/annotations/%s"}]`, annoationKeyEscaped)
		if err := r.Patch(ctx, &service, client.RawPatch(types.JSONPatchType, []byte(jsonPatch)), client.FieldOwner(FieldOwner)); err != nil {
			errors = append(errors, fmt.Errorf("could not patch service %q annotation: %w", serviceRef.Name, err))
		}
	}

	// Return aggregate errors
	return utilerrors.NewAggregate(errors)
}

func (r *ACMCertificateImportReconciler) getCertificatesFromSecret(ctx context.Context, certificateImport *acmv1alpha1.ACMCertificateImport) ([]*x509.Certificate, []byte, error) {
	// Get the secret
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: certificateImport.Namespace, Name: certificateImport.Spec.SecretRef.Name}, &secret); err != nil {
		return nil, nil, fmt.Errorf("could not get secret %q: %w", certificateImport.Spec.SecretRef.Name, err)
	}

	// Parse the certificate
	certs, err := cert.ParseCertsPEM(secret.Data["tls.crt"])
	if err != nil {
		return nil, nil, fmt.Errorf("could not load certificate from secret secret %q: %w", certificateImport.Spec.SecretRef.Name, err)
	}

	// Return the certs, the key and no error
	return certs, secret.Data["tls.key"], nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ACMCertificateImportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&acmv1alpha1.ACMCertificateImport{}).
		Complete(r)
}
