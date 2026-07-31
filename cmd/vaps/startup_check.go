package main

import (
	"context"
	"errors"
	"fmt"

	"vaps/internal/metadata"
	"vaps/internal/objectstore"
)

func validateMetadataPayloads(ctx context.Context, meta *metadata.Store, objects objectstore.Store) error {
	result, err := meta.QueryPayloads(metadata.PayloadQuery{})
	if err != nil {
		return fmt.Errorf("query metadata payloads for startup validation: %w", err)
	}
	for _, payload := range result.Items {
		remote, err := objects.Head(ctx, payload.Hash)
		if errors.Is(err, objectstore.ErrNotFound) {
			return fmt.Errorf("metadata payload %q is missing from S3", payload.Hash)
		}
		if err != nil {
			return fmt.Errorf("validate metadata payload %q in S3: %w", payload.Hash, err)
		}
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
	}
	return nil
}
