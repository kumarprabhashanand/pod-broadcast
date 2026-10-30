package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type Mode string

const (
	Once       Mode = "Once"
	Continuous Mode = "Continuous"
)

// BroadcastSpec is immutable: to change a broadcast, create a new one.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:XValidation:rule="self.mode == 'Continuous' || (!has(self.duration) && !has(self.revert))",message="duration and revert need mode Continuous"
type BroadcastSpec struct {
	Target  Target  `json:"target"`
	Request Request `json:"request"`
	// +kubebuilder:default=Once
	// +kubebuilder:validation:Enum=Once;Continuous
	Mode Mode `json:"mode,omitempty"`
	// Duration of a Continuous broadcast. Defaults to the policy's defaultDuration, else 1h.
	Duration *metav1.Duration `json:"duration,omitempty"`
	// Revert is sent to every pod the request reached when a Continuous broadcast ends. Port,
	// scheme and headers from Secrets are the request's.
	Revert *Revert `json:"revert,omitempty"`
	// +kubebuilder:default={}
	Execution Execution `json:"execution,omitempty"`
	// Seconds to keep a finished broadcast; 0 deletes it at once. Defaults to the controller's --default-ttl.
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

// Target selects the pods, in the broadcast's namespace.
// +kubebuilder:validation:XValidation:rule="[has(self.service), has(self.workloadRef), has(self.selector)].filter(x, x).size() == 1",message="set exactly one of service, workloadRef or selector"
type Target struct {
	Service     string                `json:"service,omitempty"`
	WorkloadRef *WorkloadRef          `json:"workloadRef,omitempty"`
	Selector    *metav1.LabelSelector `json:"selector,omitempty"`
	// IncludeNotReady also calls pods that aren't ready.
	IncludeNotReady bool `json:"includeNotReady,omitempty"`
}

type WorkloadRef struct {
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet;ReplicaSet
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Request struct {
	// Port name or number. Defaults to the pod's port annotation, then defaultPort, then a
	// Service's only port.
	Port *intstr.IntOrString `json:"port,omitempty"`
	// DefaultPort is the port name used when neither port nor the pod's port annotation is set.
	DefaultPort string `json:"defaultPort,omitempty"`
	// Defaults to the pod's scheme annotation, then http. Pod certificates aren't verified.
	// +kubebuilder:validation:Enum=http;https
	Scheme string `json:"scheme,omitempty"`
	// +kubebuilder:validation:Pattern=`^[A-Za-z]+$`
	Method string `json:"method"`
	// Path and query.
	// +kubebuilder:validation:Pattern=`^/\S*$`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	// HeadersFromSecrets names Secrets whose keys become headers. Each must carry the label
	// broadcast.prabhash.net/use=true and be listed in a BroadcastPolicy.
	HeadersFromSecrets []string `json:"headersFromSecrets,omitempty"`
	// +kubebuilder:validation:MaxLength=65536
	Body string `json:"body,omitempty"`
}

type Revert struct {
	// +kubebuilder:validation:Pattern=`^[A-Za-z]+$`
	Method string `json:"method"`
	// +kubebuilder:validation:Pattern=`^/\S*$`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	// +kubebuilder:validation:MaxLength=65536
	Body string `json:"body,omitempty"`
}

// Execution sets how the pods are called.
type Execution struct {
	// Timeout, Retries and Backoff are pointers, so that a zero value from a Go client gets the default.

	// Pods called at the same time.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	Concurrency int32 `json:"concurrency,omitempty"`
	// Timeout for each attempt.
	// +kubebuilder:default="5s"
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// Retries after a connection error, a timeout or status 429, 500, 502, 503 or 504.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	Retries *int32 `json:"retries,omitempty"`
	// Base delay between retries; it doubles each time, with jitter.
	// +kubebuilder:default="1s"
	Backoff *metav1.Duration `json:"backoff,omitempty"`
	// Status codes that count as success, as classes such as 2xx or codes such as 404.
	// +kubebuilder:default={"2xx"}
	SuccessCodes []string `json:"successCodes,omitempty"`
}

type Phase string

const (
	Pending         Phase = ""
	Rejected        Phase = "Rejected"
	Running         Phase = "Running"
	Succeeded       Phase = "Succeeded"
	PartiallyFailed Phase = "PartiallyFailed"
	Failed          Phase = "Failed"
	Active          Phase = "Active"
	Reverting       Phase = "Reverting"
	Completed       Phase = "Completed"
)

// Finished reports whether the broadcast will make no more calls.
func (p Phase) Finished() bool {
	switch p {
	case Rejected, Succeeded, PartiallyFailed, Failed, Completed:
		return true
	}
	return false
}

// Pod states.
const (
	PodSucceeded    = "Succeeded"
	PodFailed       = "Failed"
	PodGone         = "Gone"
	PodReverted     = "Reverted"
	PodRevertFailed = "RevertFailed"
)

type BroadcastStatus struct {
	Phase Phase `json:"phase,omitempty"`
	// Message explains a Rejected or Failed phase.
	Message    string       `json:"message,omitempty"`
	StartedAt  *metav1.Time `json:"startedAt,omitempty"`
	ExpiresAt  *metav1.Time `json:"expiresAt,omitempty"`
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	Summary    Summary      `json:"summary,omitempty"`
	Pods       []PodResult  `json:"pods,omitempty"`
}

type Summary struct {
	Targeted     int32 `json:"targeted"`
	Succeeded    int32 `json:"succeeded"`
	Failed       int32 `json:"failed"`
	Gone         int32 `json:"gone"`
	Reverted     int32 `json:"reverted,omitempty"`
	RevertFailed int32 `json:"revertFailed,omitempty"`
}

// Outcome is what the results add up to. Pods that are gone count neither way.
func (s Summary) Outcome() Phase {
	switch {
	case s.Failed == 0:
		return Succeeded
	case s.Succeeded == 0:
		return Failed
	}
	return PartiallyFailed
}

type PodResult struct {
	Name       string `json:"name"`
	UID        string `json:"uid"`
	IP         string `json:"ip,omitempty"`
	Port       int32  `json:"port,omitempty"`
	Scheme     string `json:"scheme,omitempty"`
	State      string `json:"state"`
	Code       int32  `json:"code,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Attempts   int32  `json:"attempts,omitempty"`
	Error      string `json:"error,omitempty"`
	// Body is the start of the response, at most 256 bytes.
	Body string `json:"body,omitempty"`
}

// Broadcast runs one HTTP request on every ready pod of a target.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Targeted",type=integer,JSONPath=`.status.summary.targeted`
// +kubebuilder:printcolumn:name="Succeeded",type=integer,JSONPath=`.status.summary.succeeded`
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.summary.failed`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Broadcast struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BroadcastSpec   `json:"spec"`
	Status BroadcastStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BroadcastList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Broadcast `json:"items"`
}
