package functional_test

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFunctionalConcurrentManySmallFiles(t *testing.T) {
	srv := startLoadServer(t)
	token := requestToken(t, srv.Client, srv.URL)

	type file struct {
		payload []byte
		hash    string
	}
	files := make([]file, smallFileCount)
	for i := range files {
		files[i].payload = smallPayload(i)
		files[i].hash = ioHash(files[i].payload)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, smallFileCount)
	for i := range files {
		wg.Add(1)
		go func(f file) {
			defer wg.Done()
			status, err := pushDirectStatus(srv.Client, srv.URL, token, f.hash, f.payload)
			if err != nil {
				errCh <- err
				return
			}
			if status != http.StatusCreated {
				errCh <- fmt.Errorf("push %s: status=%d", f.hash[:8], status)
				return
			}
			got, err := pullBytesStatus(srv.Client, srv.URL, token, f.hash)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(got, f.payload) {
				errCh <- fmt.Errorf("pull %s: body mismatch", f.hash[:8])
			}
		}(files[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestFunctionalConcurrentFewLargeFiles(t *testing.T) {
	srv := startLoadServer(t)
	token := requestToken(t, srv.Client, srv.URL)

	type file struct {
		payload []byte
		hash    string
	}
	files := make([]file, largeFileCount)
	for i := range files {
		files[i].payload = largePayload(largeFileSize)
		files[i].payload[0] = byte('a' + i)
		files[i].hash = ioHash(files[i].payload)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, largeFileCount)
	for i := range files {
		wg.Add(1)
		go func(f file) {
			defer wg.Done()
			status, err := pushDirectStatus(srv.Client, srv.URL, token, f.hash, f.payload)
			if err != nil {
				errCh <- err
				return
			}
			if status != http.StatusCreated {
				errCh <- fmt.Errorf("push large %s: status=%d", f.hash[:8], status)
				return
			}
			got, err := pullBytesStatus(srv.Client, srv.URL, token, f.hash)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(got, f.payload) {
				errCh <- fmt.Errorf("pull large %s: body mismatch", f.hash[:8])
			}
		}(files[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestFunctionalTimingDownloadBeforeUploadCompletes(t *testing.T) {
	srv := startLoadServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	payload := largePayload(512 * 1024)
	hash := ioHash(payload)

	startUpload := make(chan struct{})
	uploadDone := make(chan error, 1)
	go func() {
		<-startUpload
		status, err := pushDirectStatus(srv.Client, srv.URL, token, hash, payload)
		if err != nil {
			uploadDone <- err
			return
		}
		if status != http.StatusCreated {
			uploadDone <- fmt.Errorf("upload status = %d, want %d", status, http.StatusCreated)
			return
		}
		uploadDone <- nil
	}()

	status, err := pullHeadStatus(srv.Client, srv.URL, token, hash)
	if err != nil {
		t.Fatalf("HEAD before upload: %v", err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("HEAD before upload status = %d, want %d", status, http.StatusNotFound)
	}

	close(startUpload)
	select {
	case err := <-uploadDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("upload did not finish in time")
	}

	got, err := pullBytesStatus(srv.Client, srv.URL, token, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("pull body mismatch after upload completed")
	}
}

func TestFunctionalTimingConcurrentUploadAndDownload(t *testing.T) {
	srv := startLoadServer(t)
	token := requestToken(t, srv.Client, srv.URL)

	const workers = 8
	type job struct {
		payload []byte
		hash    string
	}
	jobs := make([]job, workers)
	for i := range jobs {
		jobs[i].payload = largePayload(256 * 1024)
		jobs[i].payload[0] = byte('0' + i)
		jobs[i].hash = ioHash(jobs[i].payload)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workers*2)
	for i := range jobs {
		wg.Add(2)
		go func(j job) {
			defer wg.Done()
			status, err := pushDirectStatus(srv.Client, srv.URL, token, j.hash, j.payload)
			if err != nil {
				errCh <- err
				return
			}
			if status != http.StatusCreated {
				errCh <- fmt.Errorf("upload %s: status=%d", j.hash[:8], status)
			}
		}(jobs[i])
		go func(j job) {
			defer wg.Done()
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) {
				status, err := pullHeadStatus(srv.Client, srv.URL, token, j.hash)
				if err != nil {
					errCh <- err
					return
				}
				switch status {
				case http.StatusOK:
					got, err := pullBytesStatus(srv.Client, srv.URL, token, j.hash)
					if err != nil {
						errCh <- err
						return
					}
					if !bytes.Equal(got, j.payload) {
						errCh <- fmt.Errorf("download %s: body mismatch", j.hash[:8])
					}
					return
				case http.StatusNotFound:
					time.Sleep(10 * time.Millisecond)
					continue
				default:
					errCh <- fmt.Errorf("download %s: unexpected status=%d", j.hash[:8], status)
					return
				}
			}
			errCh <- fmt.Errorf("download %s: timed out waiting for upload", j.hash[:8])
		}(jobs[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestFunctionalTimingParallelUploadSameHash(t *testing.T) {
	srv := startLoadServer(t)
	token := requestToken(t, srv.Client, srv.URL)
	payload := []byte("same-hash-parallel-upload")
	hash := ioHash(payload)

	const workers = 16
	var created int32
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := pushDirectStatus(srv.Client, srv.URL, token, hash, payload)
			if err != nil {
				errCh <- err
				return
			}
			switch status {
			case http.StatusCreated:
				atomic.AddInt32(&created, 1)
			case http.StatusOK:
			default:
				errCh <- fmt.Errorf("parallel same-hash push: status=%d", status)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("created count = %d, want 1", created)
	}
	got, err := pullBytesStatus(srv.Client, srv.URL, token, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("pull body mismatch")
	}
}
