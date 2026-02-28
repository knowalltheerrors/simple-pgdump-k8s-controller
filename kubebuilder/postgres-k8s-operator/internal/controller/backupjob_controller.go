package controller

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	dbopsv1alpha1 "github.com/knowalltheerrors/postgres-k8s-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// +kubebuilder:rbac:groups=dbops.example.com,resources=backupjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dbops.example.com,resources=backupjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dbops.example.com,resources=backupjobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

type BackupJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *BackupJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr dbopsv1alpha1.BackupJob
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Ignore deletes for now (simple operator)
	if !cr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	applyDefaults(&cr)

	// If no Job created yet, create one.
	if cr.Status.K8sJobName == "" {
		jobName := buildJobName(cr.Name, cr.Generation)
		objectKey := buildObjectKey(cr)

		job := r.buildK8sJob(&cr, jobName, objectKey)
		if err := controllerutil.SetControllerReference(&cr, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		err := r.Create(ctx, job)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}

		now := metav1.Now()
		cr.Status.K8sJobName = jobName
		cr.Status.ObjectKey = objectKey
		cr.Status.Phase = dbopsv1alpha1.BackupPhasePending
		cr.Status.Message = "Kubernetes Job created"
		cr.Status.StartedAt = &now
		cr.Status.LastRunTime = &now

		if err := r.Status().Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Created backup job", "backupCR", cr.Name, "job", jobName, "objectKey", objectKey)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	var k8sJob batchv1.Job
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      cr.Status.K8sJobName,
	}, &k8sJob); err != nil {
		if apierrors.IsNotFound(err) {
			cr.Status.Phase = dbopsv1alpha1.BackupPhaseFailed
			cr.Status.Message = "Kubernetes Job not found"
			now := metav1.Now()
			cr.Status.FinishedAt = &now
			_ = r.Status().Update(ctx, &cr)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if isJobComplete(&k8sJob) {
		if cr.Status.Phase != dbopsv1alpha1.BackupPhaseSucceeded {
			now := metav1.Now()
			cr.Status.Phase = dbopsv1alpha1.BackupPhaseSucceeded
			cr.Status.Message = "Backup completed successfully"
			cr.Status.FinishedAt = &now
			if err := r.Status().Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if isJobFailed(&k8sJob) {
		if cr.Status.Phase != dbopsv1alpha1.BackupPhaseFailed {
			now := metav1.Now()
			cr.Status.Phase = dbopsv1alpha1.BackupPhaseFailed
			cr.Status.Message = "Backup job failed"
			cr.Status.FinishedAt = &now
			if err := r.Status().Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if cr.Status.Phase != dbopsv1alpha1.BackupPhaseRunning {
		cr.Status.Phase = dbopsv1alpha1.BackupPhaseRunning
		cr.Status.Message = "Backup job is running"
		if err := r.Status().Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *BackupJobReconciler) buildK8sJob(cr *dbopsv1alpha1.BackupJob, jobName, objectKey string) *batchv1.Job {
	backoff := int32(0)
	ttl := int32(360)
	if cr.Spec.TTLSecondsAfterFinished != nil {
		ttl = *cr.Spec.TTLSecondsAfterFinished
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       "postgres-backup",
		"app.kubernetes.io/managed-by": "postgres-k8s-operator",
		"dbops.example.com/backup-cr":  cr.Name,
	}

	env := []corev1.EnvVar{
		{Name: "PGHOST", Value: cr.Spec.Postgres.Host},
		{Name: "PGPORT", Value: fmt.Sprintf("%d", cr.Spec.Postgres.Port)},
		{Name: "PGDATABASE", Value: cr.Spec.Postgres.Database},
		{Name: "PGUSER", Value: cr.Spec.Postgres.Username},
		{Name: "PGSSLMODE", Value: defaultString(cr.Spec.Postgres.SSLMode, "disable")},

		{Name: "S3_PROVIDER", Value: strings.ToLower(cr.Spec.Storage.Provider)},
		{Name: "S3_BUCKET", Value: cr.Spec.Storage.Bucket},
		{Name: "S3_PREFIX", Value: cr.Spec.Storage.Prefix},
		{Name: "S3_OBJECT_KEY", Value: objectKey},
		{Name: "S3_ENDPOINT", Value: cr.Spec.Storage.Endpoint},
		{Name: "AWS_REGION", Value: defaultString(cr.Spec.Storage.Region, "us-east-1")},
		{Name: "S3_FORCE_PATH_STYLE", Value: fmt.Sprintf("%t", cr.Spec.Storage.ForcePathStyle)},

		{
			Name: "PGPASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: cr.Spec.Postgres.PasswordSecretRef.Name},
					Key:                  cr.Spec.Postgres.PasswordSecretRef.Key,
				},
			},
		},
		{
			Name: "AWS_ACCESS_KEY_ID",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: cr.Spec.Storage.AccessKeySecretRef.Name},
					Key:                  cr.Spec.Storage.AccessKeySecretRef.Key,
				},
			},
		},
		{
			Name: "AWS_SECRET_ACCESS_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: cr.Spec.Storage.SecretKeySecretRef.Name},
					Key:                  cr.Spec.Storage.SecretKeySecretRef.Key,
				},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: cr.Spec.ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:            "backup-runner",
							Image:           defaultString(cr.Spec.Image, "docker.io/coderunner777/pgdumpk8s:latest"),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             env,
						},
					},
				},
			},
		},
	}

	return job
}

func (r *BackupJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbopsv1alpha1.BackupJob{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func applyDefaults(cr *dbopsv1alpha1.BackupJob) {
	if cr.Spec.Postgres.Port == 0 {
		cr.Spec.Postgres.Port = 5432
	}
	if cr.Spec.Storage.Region == "" {
		cr.Spec.Storage.Region = "us-east-1"
	}
	if cr.Spec.Storage.Provider == "" {
		cr.Spec.Storage.Provider = "s3"
	}
}

func buildJobName(crName string, generation int64) string {
	name := fmt.Sprintf("%s-%d", crName, generation)
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func buildObjectKey(cr dbopsv1alpha1.BackupJob) string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	file := fmt.Sprintf("%s_%s.dump", cr.Spec.Postgres.Database, ts)
	// prefix/namespace/crname/file
	return path.Clean(path.Join("/", cr.Spec.Storage.Prefix, cr.Namespace, cr.Name, file))[1:]
}

func isJobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
