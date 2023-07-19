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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ACMCertificateImportSpec defines the desired state of ACMCertificateImport
type ACMCertificateImportSpec struct {
	// If the import is frozen no update will take place, the certificate will also not be deleted if the
	// ACMCertificateImport is deleted. Services will continue to be updated.
	Frozen *bool `json:"frozen,omitempty"`
	// The secret to load the certificate from, the secret must have the cert be under the "tls.crt" key and the private
	// key be under the "tls.key" key.
	SecretRef corev1.LocalObjectReference `json:"secretRef"`
	// ServiceRefs are services that should be updated with the ACM annotation to have AWS use the certificate for their
	// load balancer.
	ServiceRefs []corev1.LocalObjectReference `json:"serviceRefs,omitempty"`
}

// ACMCertificateImportStatus defines the observed state of ACMCertificateImport
type ACMCertificateImportStatus struct {
	ARN          *string `json:"arn,omitempty"`
	SerialNumber string  `json:"serialNumber"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ACMCertificateImport is the Schema for the acmcertificateimports API
type ACMCertificateImport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ACMCertificateImportSpec   `json:"spec,omitempty"`
	Status ACMCertificateImportStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ACMCertificateImportList contains a list of ACMCertificateImport
type ACMCertificateImportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ACMCertificateImport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ACMCertificateImport{}, &ACMCertificateImportList{})
}
