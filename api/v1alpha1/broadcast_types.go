package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Mode says whether a broadcast calls the pods once, or keeps calling pods that become ready.
type Mode string

const (
	Once       Mode = "Once"
	Continuous Mode = "Continuous"
)

// BroadcastSpec says what to call and how. It is immutable: to change a broadcast, create a new one.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:XValidation:rule="self.mode == 'Continuous' || (!has(self.duration) && !has(self.revert))",message="duration and revert need mode Continuous"
type BroadcastSpec struct {
	// Target selects the pods to call.
	Target Target `json:"target"`
	// Request is sent to every pod of the target.
	Request Request `json:"request"`
	// Mode is Once, to call the ready pods once, or Continuous, to also call pods that become ready
	// until the duration ends, then send the revert.
	// +kubebuilder:default=Once
	// +kubebuilder:validation:Enum=Once;Continuous
	Mode Mode `json:"mode,omitempty"`
	// Duration of a Continuous broadcast. Defaults to the policy's defaultDuration, else 1h.
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="duration must be positive"
	Duration *metav1.Duration `json:"duration,omitempty"`
	// Revert is sent to every pod the request reached when a Continuous broadcast ends. Port,
	// scheme and headers from Secrets are the request's.
	Revert *Revert `json:"revert,omitempty"`
	// Execution sets concurrency, timeouts, retries and success codes.
	// +kubebuilder:default={}
	Execution Execution `json:"execution,omitempty"`
	// Seconds to keep a finished broadcast; 0 deletes it at once. Defaults to the controller's --default-ttl.
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

// Target selects the pods, in the broadcast's namespace.
// +kubebuilder:validation:XValidation:rule="[has(self.service), has(self.workloadRef), has(self.selector)].filter(x, x).size() == 1",message="set exactly one of service, workloadRef or selector"
type Target struct {
	// Service calls the pods behind this Service, taken from its EndpointSlices.
	Service string `json:"service,omitempty"`
	// WorkloadRef calls the pods matching a workload's selector.
	WorkloadRef *WorkloadRef `json:"workloadRef,omitempty"`
	// Selector calls the pods matching these labels. It can't be empty.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// IncludeNotReady also calls pods that aren't ready.
	IncludeNotReady bool `json:"includeNotReady,omitempty"`
}

// WorkloadRef names a workload in the broadcast's namespace.
type WorkloadRef struct {
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet;ReplicaSet
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Request is the HTTP request sent to each pod.
type Request struct {
	// Port name or number. Defaults to the pod's port annotation, then defaultPort, then a
	// Service's only port.
	Port *intstr.IntOrString `json:"port,omitempty"`
	// DefaultPort is the port name used when neither port nor the pod's port annotation is set.
	DefaultPort string `json:"defaultPort,omitempty"`
	// Scheme is http or https. Defaults to the pod's scheme annotation, then http. Pod certificates
	// aren't verified.
	// +kubebuilder:validation:Enum=http;https
	Scheme string `json:"scheme,omitempty"`
	// Method is the HTTP method, such as GET or POST.
	// +kubebuilder:validation:Pattern=`^[A-Za-z]+$`
	Method string `json:"method"`
	// Path and query. Where a BroadcastPolicy applies, the path can't have "." or ".." segments,
	// ";", "#", a backslash, or percent-encoded ".", "/", backslash or "%".
	// +kubebuilder:validation:Pattern=`^/\S*$`
	Path string `json:"path"`
	// Headers are sent as they are, and anyone who can read the Broadcast sees them. Put secret
	// values in headersFromSecrets instead.
	Headers map[string]string `json:"headers,omitempty"`
	// HeadersFromSecrets names Secrets whose keys become headers. Each must carry the label
	// broadcast.prabhash.net/use=true and be listed in a BroadcastPolicy.
	HeadersFromSecrets []string `json:"headersFromSecrets,omitempty"`
	// Body is sent as it is, at most 64 KiB.
	// +kubebuilder:validation:MaxLength=65536
	Body string `json:"body,omitempty"`
}

// Revert is the request that undoes a Continuous broadcast. Its port, scheme and headers from
// Secrets are the request's.
type Revert struct {
	// +kubebuilder:validation:Pattern=`^[A-Za-z]+$`
	Method string `json:"method"`
	// Path and query, with the same rules as the request's.
	// +kubebuilder:validation:Pattern=`^/\S*$`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	// +kubebuilder:validation:MaxLength=65536
	Body string `json:"body,omitempty"`
}

// Execution sets how the pods are called. Timeout, Retries and Backoff are pointers, so that a zero
// value from a Go client gets the default.
type Execution struct {
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

// Phase is where a broadcast is in its life. The empty phase is Pending: not looked at yet.
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

// ConditionComplete is True once a broadcast has finished, whatever the result; its reason is the
// phase. Use kubectl broadcast wait for exit codes that reflect the result.
const ConditionComplete = "Complete"

// BroadcastStatus is what the controller has done so far.
type BroadcastStatus struct {
	Phase Phase `json:"phase,omitempty"`
	// Message explains a Rejected or Failed phase, or why a Continuous broadcast ended.
	Message string `json:"message,omitempty"`
	// Conditions holds Complete.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	StartedAt  *metav1.Time       `json:"startedAt,omitempty"`
	// ExpiresAt is when a Continuous broadcast ends.
	ExpiresAt  *metav1.Time `json:"expiresAt,omitempty"`
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	// Summary counts the pods by result.
	Summary Summary `json:"summary,omitempty"`
	// Pods holds one entry per pod called, at most 1,000.
	Pods []PodResult `json:"pods,omitempty"`
	// DroppedPods counts pods that went away and whose entries were removed from pods to keep the
	// status small. They count as gone in the summary.
	DroppedPods int32 `json:"droppedPods,omitempty"`
}

// Summary counts pods by result.
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

// PodResult is the outcome on one pod.
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
// +kubebuilder:resource:shortName=bc,categories=pod-broadcast
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

// BroadcastList is a list of Broadcasts.
// +kubebuilder:object:root=true
type BroadcastList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Broadcast `json:"items"`
}
