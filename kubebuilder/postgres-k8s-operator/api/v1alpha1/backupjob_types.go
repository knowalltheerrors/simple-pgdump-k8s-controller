package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type PostgresSpec struct {
	Host              string       `json:"host"`
	Port              int32        `json:"port,omitempty"`
	Database          string       `json:"database"`
	Username          string       `json:"username"`
	PasswordSecretRef SecretKeyRef `json:"passwordSecretRef"`
	// currently only disable and require is supported ! (No verify supported, will be added ASAP.)
	SSLMode string `json:"sslMode,omitempty"` // disable, require, verify-ca, verify-full
}

type StorageSpec struct {
	// s3 or minio (both go through S3 API client)
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix,omitempty"`

	// AWS S3: keep Endpoint empty (AWS default endpoint resolution)
	// MinIO: set Endpoint, e.g. https://minio.minio.svc:9000
	Endpoint string `json:"endpoint,omitempty"`
	Region   string `json:"region,omitempty"`

	ForcePathStyle bool `json:"forcePathStyle,omitempty"`

	AccessKeySecretRef SecretKeyRef `json:"accessKeySecretRef"`
	SecretKeySecretRef SecretKeyRef `json:"secretKeySecretRef"`
}

type BackupJobSpec struct {
	Postgres PostgresSpec `json:"postgres"`
	Storage  StorageSpec  `json:"storage"`

	// Backup runner image (contains pg_dump + Go backup binary)
	Image string `json:"image,omitempty"`

	// If empty, operator defaults it
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// Optional SA for the k8s Job pod
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Extra environment variables to pass to the backup Job pod.
	// Example usage: HTTP_PROXY / HTTPS_PROXY / NO_PROXY.
	ExtraEnv map[string]string `json:"extraEnv,omitempty"`
}

type BackupPhase string

const (
	BackupPhasePending   BackupPhase = "Pending"
	BackupPhaseRunning   BackupPhase = "Running"
	BackupPhaseSucceeded BackupPhase = "Succeeded"
	BackupPhaseFailed    BackupPhase = "Failed"
)

type BackupJobStatus struct {
	Phase       BackupPhase  `json:"phase,omitempty"`
	Message     string       `json:"message,omitempty"`
	K8sJobName  string       `json:"k8sJobName,omitempty"`
	ObjectKey   string       `json:"objectKey,omitempty"`
	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	FinishedAt  *metav1.Time `json:"finishedAt,omitempty"`
	LastRunTime *metav1.Time `json:"lastRunTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pgbk
type BackupJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupJobSpec   `json:"spec,omitempty"`
	Status BackupJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackupJob{}, &BackupJobList{})
}
