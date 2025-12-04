package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProxySpec defines the desired state of Proxy.
type ProxySpec struct {
	// +kubebuilder:validation:Required
	URL string `json:"url"`
}

// ProxyStatus defines the observed state of Proxy.
type ProxyStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Proxy is the Schema for the proxies API.
type Proxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxySpec   `json:"spec,omitempty"`
	Status ProxyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProxyList contains a list of Proxy.
type ProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Proxy `json:"items"`
}

// RedirectSpec defines the desired state of Redirect.
type RedirectSpec struct {
	// +kubebuilder:validation:Required
	URL string `json:"url"`
}

// RedirectStatus defines the observed state of Redirect.
type RedirectStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Redirect is the Schema for the redirects API.
type Redirect struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedirectSpec   `json:"spec,omitempty"`
	Status RedirectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedirectList contains a list of Redirect.
type RedirectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Redirect `json:"items"`
}

// BasicAuthSpec defines the desired state of BasicAuth.
type BasicAuthSpec struct {
	// +kubebuilder:validation:Required
	SecretKeyRef                 corev1.SecretKeySelector `json:"secretKeyRef"`
	networkingv1.HTTPIngressPath `json:",inline"`
}

// BasicAuthStatus defines the observed state of BasicAuth.
type BasicAuthStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// BasicAuth is the Schema for the basicauths API.
type BasicAuth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BasicAuthSpec   `json:"spec,omitempty"`
	Status BasicAuthStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BasicAuthList contains a list of BasicAuth.
type BasicAuthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BasicAuth `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Proxy{}, &ProxyList{}, &Redirect{}, &RedirectList{}, &BasicAuth{}, &BasicAuthList{})
}
