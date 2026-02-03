package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "test.kopper.io", Version: "v1"}
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&TestResource{},
		&TestResourceList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

type TestResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TestResourceSpec   `json:"spec,omitempty"`
	Status TestResourceStatus `json:"status,omitempty"`
}

type TestResourceSpec struct {
	// Message is a simple string field
	Message string `json:"message,omitempty"`
	// Headers is an array field that replaces the old map-style headers.
	Headers []Header `json:"headers,omitempty"`
}

type Header struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type TestResourceStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true

type TestResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TestResource `json:"items"`
}

// DeepCopyInto copies the receiver into out.
func (in *TestResource) DeepCopyInto(out *TestResource) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy creates a deep copy of TestResource.
func (in *TestResource) DeepCopy() *TestResource {
	if in == nil {
		return nil
	}
	out := new(TestResource)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a deep copy as runtime.Object.
func (in *TestResource) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto copies the receiver into out.
func (in *TestResourceSpec) DeepCopyInto(out *TestResourceSpec) {
	*out = *in
	if in.Headers != nil {
		in, out := &in.Headers, &out.Headers
		*out = make([]Header, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy creates a deep copy of TestResourceSpec.
func (in *TestResourceSpec) DeepCopy() *TestResourceSpec {
	if in == nil {
		return nil
	}
	out := new(TestResourceSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies the receiver into out.
func (in *TestResourceList) DeepCopyInto(out *TestResourceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]TestResource, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a deep copy of TestResourceList.
func (in *TestResourceList) DeepCopy() *TestResourceList {
	if in == nil {
		return nil
	}
	out := new(TestResourceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a deep copy as runtime.Object.
func (in *TestResourceList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
