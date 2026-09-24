/*
Copyright 2022.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ForgejoCommentSpec defines the desired state of ForgejoComment
type ForgejoCommentSpec struct {
	ForgejoPullRequestRef `json:"forgejo"`
	CommentSpec           `json:"comment"`

	// Suspend can be used to suspend the reconciliation of this object
	// +optional
	// +kubebuilder:default:=false
	Suspend bool `json:"suspend"`
}

// ForgejoCommentStatus defines the observed state of ForgejoComment
type ForgejoCommentStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	CommentId string `json:"commentId,omitempty"`

	// +optional
	LastPostedBodyHash string `json:"lastPostedBodyHash,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ForgejoComment is the Schema for the forgejocomments API
type ForgejoComment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ForgejoCommentSpec   `json:"spec,omitempty"`
	Status ForgejoCommentStatus `json:"status,omitempty"`
}

func (fc *ForgejoComment) GetCommentSourceSpec() *CommentSourceSpec {
	return &fc.Spec.Source
}

//+kubebuilder:object:root=true

// ForgejoCommentList contains a list of ForgejoComment
type ForgejoCommentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ForgejoComment `json:"items"`
}

func (l *ForgejoCommentList) GetItems() []client.Object {
	ret := make([]client.Object, len(l.Items))
	for i := range l.Items {
		ret[i] = &l.Items[i]
	}
	return ret
}

func init() {
	SchemeBuilder.Register(&ForgejoComment{}, &ForgejoCommentList{})
}
