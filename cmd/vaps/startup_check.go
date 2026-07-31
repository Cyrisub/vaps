package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/cpuid/v2"
	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

const (
	defaultMetadataValidationWorkers          = -1
	defaultMetadataValidationTimeout          = 10 * time.Second
	defaultMetadataValidationProgressInterval = 10 * time.Second
)

type metadataValidationOptions struct {
	Workers          int
	RequestTimeout   time.Duration
	ProgressInterval time.Duration
}

func validateMetadataPayloads(ctx context.Context, meta *metadata.Store, objects objectstore.Store) error {
	return validateMetadataPayloadsWithOptions(ctx, meta, objects, metadataValidationOptions{
		Workers:          defaultMetadataValidationWorkers,
		RequestTimeout:   defaultMetadataValidationTimeout,
		ProgressInterval: defaultMetadataValidationProgressInterval,
	})
}

func validateMetadataPayloadsWithOptions(
	ctx context.Context,
	meta *metadata.Store,
	objects objectstore.Store,
	options metadataValidationOptions,
) error {
	options = normalizeMetadataValidationOptions(options)
	startedAt := time.Now()
	log.Printf("metadata validation loading payloads")
	result, err := meta.QueryPayloads(metadata.PayloadQuery{})
	if err != nil {
		return fmt.Errorf("query metadata payloads for startup validation: %w", err)
	}
	total := len(result.Items)
	log.Printf(
		"metadata validation started payload_count=%d workers=%d request_timeout=%s progress_interval=%s",
		total,
		options.Workers,
		options.RequestTimeout,
		options.ProgressInterval,
	)
	if total == 0 {
		log.Printf("metadata validation completed payload_count=0 duration=%s", formatElapsed(time.Since(startedAt)))
		return nil
	}

	if lister, ok := objects.(objectstore.ListStore); ok {
		return validateMetadataPayloadsWithList(ctx, meta, objects, result.Items, lister, options, startedAt)
	}
	err = validateMetadataPayloadsWithHead(ctx, meta, objects, result.Items, options, nil, total, startedAt, true)
	if err == nil {
		log.Printf(
			"metadata validation completed payload_count=%d checked=%d duration=%s",
			total,
			total,
			formatElapsed(time.Since(startedAt)),
		)
	}
	return err
}

func validateMetadataPayloadsWithList(
	ctx context.Context,
	meta *metadata.Store,
	objects objectstore.Store,
	payloads []metadata.Payload,
	lister objectstore.ListStore,
	options metadataValidationOptions,
	startedAt time.Time,
) error {
	listStartedAt := time.Now()
	log.Printf("metadata validation listing objects")
	remoteObjects, err := lister.List(ctx)
	if err != nil {
		return fmt.Errorf("list objects for startup validation: %w", err)
	}
	remoteByHash := make(map[string]objectstore.Info, len(remoteObjects))
	for _, remote := range remoteObjects {
		remoteByHash[remote.Hash] = remote
	}

	var checked atomic.Int64
	missingETag := make([]metadata.Payload, 0)
	for _, payload := range payloads {
		remote, ok := remoteByHash[payload.Hash]
		if !ok {
			return fmt.Errorf("metadata payload %q is missing from S3", payload.Hash)
		}
		if remote.Size != payload.Size {
			return fmt.Errorf(
				"metadata payload %q does not match S3 object size %d",
				payload.Hash,
				remote.Size,
			)
		}
		if payload.ETag == "" {
			missingETag = append(missingETag, payload)
			continue
		}
		if remote.ETag == "" || remote.ETag != payload.ETag {
			return fmt.Errorf(
				"metadata payload %q does not match S3 object ETag %q",
				payload.Hash,
				remote.ETag,
			)
		}
		checked.Add(1)
	}
	log.Printf(
		"metadata validation listed object_count=%d missing_etag=%d duration=%s",
		len(remoteObjects),
		len(missingETag),
		formatElapsed(time.Since(listStartedAt)),
	)
	if len(missingETag) > 0 {
		if err := validateMetadataPayloadsWithHead(
			ctx,
			meta,
			objects,
			missingETag,
			options,
			&checked,
			len(payloads),
			startedAt,
			true,
		); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("metadata validation canceled: %w", err)
	}
	log.Printf(
		"metadata validation completed payload_count=%d checked=%d duration=%s",
		len(payloads),
		checked.Load(),
		formatElapsed(time.Since(startedAt)),
	)
	return nil
}

func validateMetadataPayloadsWithHead(
	ctx context.Context,
	meta *metadata.Store,
	objects objectstore.Store,
	payloads []metadata.Payload,
	options metadataValidationOptions,
	checked *atomic.Int64,
	total int,
	startedAt time.Time,
	persistETag bool,
) error {
	if checked == nil {
		checked = new(atomic.Int64)
	}
	validationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan metadata.Payload)
	var workerWG sync.WaitGroup
	var progressWG sync.WaitGroup
	var firstErr error
	var firstErrMu sync.Mutex

	recordError := func(err error) bool {
		firstErrMu.Lock()
		defer firstErrMu.Unlock()
		if firstErr != nil {
			return false
		}
		firstErr = err
		cancel()
		return true
	}

	worker := func() {
		defer workerWG.Done()
		for {
			select {
			case <-validationCtx.Done():
				return
			case payload, ok := <-jobs:
				if !ok {
					return
				}
				requestStarted := time.Now()
				requestCtx, requestCancel := context.WithTimeout(validationCtx, options.RequestTimeout)
				remote, err := objects.Head(requestCtx, payload.Hash)
				requestCancel()
				if errors.Is(err, objectstore.ErrNotFound) {
					err = fmt.Errorf("metadata payload %q is missing from S3", payload.Hash)
				} else if err != nil {
					err = fmt.Errorf("validate metadata payload %q in S3: %w", payload.Hash, err)
				} else {
					err = validateMetadataPayloadInfo(payload, remote)
				}
				if err == nil && payload.ETag != "" && remote.ETag != payload.ETag {
					err = fmt.Errorf(
						"metadata payload %q does not match S3 object ETag %q",
						payload.Hash,
						remote.ETag,
					)
				}
				if err == nil && persistETag && payload.ETag == "" {
					if remote.ETag == "" {
						err = fmt.Errorf("metadata payload %q has no S3 object ETag", payload.Hash)
					} else {
						payload.ETag = remote.ETag
						err = meta.PutPayload(payload)
					}
				}
				checkedCount := checked.Add(1)
				if err != nil {
					if recordError(err) {
						log.Printf(
							"metadata validation failed hash=%q checked=%d/%d duration=%s error=%q",
							payload.Hash,
							checkedCount,
							total,
							formatElapsed(time.Since(requestStarted)),
							err,
						)
					}
					return
				}
			}
		}
	}

	workerWG.Add(options.Workers)
	for range options.Workers {
		go worker()
	}

	progressWG.Add(1)
	go func() {
		defer progressWG.Done()
		ticker := time.NewTicker(options.ProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				checkedCount := checked.Load()
				log.Printf(
					"metadata validation progress checked=%d/%d remaining=%d elapsed=%s",
					checkedCount,
					total,
					int64(total)-checkedCount,
					formatElapsed(time.Since(startedAt)),
				)
			case <-validationCtx.Done():
				return
			}
		}
	}()

sendJobs:
	for _, payload := range payloads {
		select {
		case jobs <- payload:
		case <-validationCtx.Done():
			break sendJobs
		}
	}
	close(jobs)
	workerWG.Wait()
	closeProgress := func() {
		cancel()
		progressWG.Wait()
	}
	closeProgress()

	firstErrMu.Lock()
	err := firstErr
	firstErrMu.Unlock()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("metadata validation canceled: %w", err)
	}
	return nil
}

func formatElapsed(duration time.Duration) string {
	return duration.Round(time.Second).String()
}

func normalizeMetadataValidationOptions(options metadataValidationOptions) metadataValidationOptions {
	switch {
	case options.Workers == -1:
		options.Workers = physicalCoreCount()
	case options.Workers == 0:
		options.Workers = 1
	case options.Workers < -1:
		options.Workers = physicalCoreCount()
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = defaultMetadataValidationTimeout
	}
	if options.ProgressInterval <= 0 {
		options.ProgressInterval = defaultMetadataValidationProgressInterval
	}
	return options
}

func physicalCoreCount() int {
	if cpuid.CPU.PhysicalCores > 0 {
		return cpuid.CPU.PhysicalCores
	}
	if count := runtime.NumCPU(); count > 0 {
		return count
	}
	return 1
}

func validateMetadataPayload(ctx context.Context, objects objectstore.Store, payload metadata.Payload) error {
	remote, err := objects.Head(ctx, payload.Hash)
	if errors.Is(err, objectstore.ErrNotFound) {
		return fmt.Errorf("metadata payload %q is missing from S3", payload.Hash)
	}
	if err != nil {
		return fmt.Errorf("validate metadata payload %q in S3: %w", payload.Hash, err)
	}
	return validateMetadataPayloadInfo(payload, remote)
}

func validateMetadataPayloadInfo(payload metadata.Payload, remote objectstore.Info) error {
	if remote.Hash != payload.Hash {
		return fmt.Errorf(
			"metadata payload %q does not match S3 object hash %q",
			payload.Hash,
			remote.Hash,
		)
	}
	if remote.Checksum != payload.Checksum {
		return fmt.Errorf(
			"metadata payload %q does not match S3 object checksum %q",
			payload.Hash,
			remote.Checksum,
		)
	}
	if remote.Size != payload.Size {
		return fmt.Errorf(
			"metadata payload %q does not match S3 object size %d",
			payload.Hash,
			remote.Size,
		)
	}
	return nil
}
