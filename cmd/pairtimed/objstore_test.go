// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

// TestReadyzChecksObjectStorage runs serve against the in-process S3
// server and reads /readyz, which checks both buckets (DESIGN 10.4).
func TestReadyzChecksObjectStorage(t *testing.T) {
	srv := objstoretest.New(t, "pairtime", "pairtime-snapshots")
	tests := []struct {
		name           string
		snapshotBucket string
		secret         string
		wantCode       int
		wantBody       string // a regular expression the body must match
	}{
		{
			name:           "/readyz is ready when both buckets answer",
			snapshotBucket: "pairtime-snapshots",
			secret:         objstoretest.SecretAccessKey,
			wantCode:       http.StatusOK,
			wantBody:       `"ready":true.*"name":"object_storage.bucket","ok":true.*"name":"object_storage.snapshot_bucket","ok":true`,
		},
		{
			name:           "/readyz fails when the snapshot bucket is missing, and names it",
			snapshotBucket: "pairtime-missing",
			secret:         objstoretest.SecretAccessKey,
			wantCode:       http.StatusServiceUnavailable,
			wantBody:       `"name":"object_storage.bucket","ok":true.*"name":"object_storage.snapshot_bucket","ok":false,"error":"[^"]*NoSuchBucket`,
		},
		{
			name:           "/readyz fails on a wrong secret and doesn't print it",
			snapshotBucket: "pairtime-snapshots",
			secret:         "wrong-secret-hunter2",
			wantCode:       http.StatusServiceUnavailable,
			wantBody:       `"name":"object_storage.bucket","ok":false,"error":"[^"]*SignatureDoesNotMatch`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, _ := writeConfig(t)
			section := "\n[object_storage]\nendpoint = \"" + srv.URL + "\"\nbucket = \"pairtime\"\nsnapshot_bucket = \"" + tt.snapshotBucket + "\"\n"
			f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(section); err != nil {
				t.Fatal(err)
			}
			f.Close()

			e, _, stderr := testEnv("PAIRTIME_S3_ACCESS_KEY_ID="+objstoretest.AccessKeyID, "PAIRTIME_S3_SECRET_ACCESS_KEY="+tt.secret)
			opsAddr := make(chan string, 1)
			e.listening = func(_, ops string) { opsAddr <- ops }
			ctx, cancel := context.WithCancel(context.Background())
			exited := make(chan int, 1)
			go func() { exited <- run(ctx, []string{"serve", "-config", configPath}, e) }()
			defer func() {
				cancel()
				select {
				case <-exited:
				case <-time.After(60 * time.Second):
					t.Error("serve didn't stop within a minute")
				}
			}()
			var addr string
			select {
			case addr = <-opsAddr:
			case code := <-exited:
				t.Fatalf("serve exited with %d before listening; stderr:\n%s", code, stderr.String())
			case <-time.After(60 * time.Second):
				t.Fatalf("serve didn't start within a minute; stderr:\n%s", stderr.String())
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get("http://" + addr + "/readyz")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("/readyz = %d, want %d; body:\n%s", resp.StatusCode, tt.wantCode, body)
			}
			if !regexp.MustCompile(tt.wantBody).Match(body) {
				t.Errorf("/readyz body doesn't match %s:\n%s", tt.wantBody, body)
			}
			for _, out := range []string{string(body), stderr.String()} {
				if strings.Contains(out, tt.secret) {
					t.Errorf("the secret was printed:\n%s", out)
				}
			}
			if !strings.Contains(stderr.String(), "snapshot_bucket="+tt.snapshotBucket) {
				t.Errorf("the log doesn't name the buckets:\n%s", stderr.String())
			}
		})
	}
}

// TestServeRunsWithoutObjectStorage checks the log says so when
// pairtime.toml has no [object_storage]: local runs and tests work
// without buckets, and a deployment can see what it left out.
func TestServeRunsWithoutObjectStorage(t *testing.T) {
	tests := []struct {
		name, wantLog string
	}{
		{name: "serve logs that object storage is off", wantLog: "object storage is off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, _ := writeConfig(t)
			e, _, stderr := testEnv()
			up := make(chan struct{}, 1)
			e.listening = func(_, _ string) { up <- struct{}{} }
			ctx, cancel := context.WithCancel(context.Background())
			exited := make(chan int, 1)
			go func() { exited <- run(ctx, []string{"serve", "-config", configPath}, e) }()
			select {
			case <-up:
			case code := <-exited:
				t.Fatalf("serve exited with %d; stderr:\n%s", code, stderr.String())
			case <-time.After(60 * time.Second):
				t.Fatal("serve didn't start within a minute")
			}
			cancel()
			<-exited
			if !strings.Contains(stderr.String(), tt.wantLog) {
				t.Errorf("stderr doesn't contain %q:\n%s", tt.wantLog, stderr.String())
			}
		})
	}
}
