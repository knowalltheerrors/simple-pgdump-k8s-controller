package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	ctx := context.Background()

	pgHost := mustEnv("PGHOST")
	pgPort := envDefault("PGPORT", "5432")
	pgDB := mustEnv("PGDATABASE")
	pgUser := mustEnv("PGUSER")
	pgPass := mustEnv("PGPASSWORD")
	pgSSLMode := envDefault("PGSSLMODE", "disable")

	bucket := mustEnv("S3_BUCKET")
	objectKey := mustEnv("S3_OBJECT_KEY")
	region := envDefault("AWS_REGION", "us-east-1")
	endpoint := os.Getenv("S3_ENDPOINT")
	forcePathStyle := envBool("S3_FORCE_PATH_STYLE", false)

	tmpDir := "/tmp"
	dumpFile := filepath.Join(tmpDir, fmt.Sprintf("%s-%d.dump", pgDB, time.Now().Unix()))

	log.Printf("Starting pg_dump host=%s port=%s db=%s user=%s sslmode=%s", pgHost, pgPort, pgDB, pgUser, pgSSLMode)

	// pg_dump -Fc creates custom format dump (compressed/portable for pg_restore)
	cmd := exec.Command(
		"pg_dump",
		"-h", pgHost,
		"-p", pgPort,
		"-U", pgUser,
		"-d", pgDB,
		"-Fc",
		"-f", dumpFile,
	)

	cmd.Env = append(os.Environ(),
		"PGPASSWORD="+pgPass,
		"PGSSLMODE="+pgSSLMode,
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Fatalf("pg_dump failed: %v", err)
	}

	f, err := os.Open(dumpFile)
	if err != nil {
		log.Fatalf("open dump file failed: %v", err)
	}
	defer f.Close()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		log.Fatalf("aws config load failed: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = forcePathStyle
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})

	uploader := manager.NewUploader(s3Client)

	log.Printf("Uploading dump to bucket=%s key=%s endpoint=%s", bucket, objectKey, endpoint)

	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(objectKey),
		Body:   f,
	})
	if err != nil {
		log.Fatalf("upload failed: %v", err)
	}

	log.Printf("Backup uploaded successfully: s3://%s/%s", bucket, objectKey)

	_ = os.Remove(dumpFile)
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing required env: %s", k)
	}
	return v
}

func envDefault(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func envBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
