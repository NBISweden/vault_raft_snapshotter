package snapshot

import (
	"context"
	"fmt"
	"io"
	"sort"

	log "github.com/sirupsen/logrus"

	"vault_raft_snapshotter/config"

	"github.com/Azure/azure-storage-blob-go/azblob"
)

// CreateAzureSnapshot writes snapshot to azure blob storage
func (s *Snapshotter) CreateAzureSnapshot(reader io.Reader, config *config.Configuration, currentTs int64) (string, error) {
	ctx := context.Background()
	url := fmt.Sprintf("raft_snapshot-%d.snap", currentTs)
	blob := s.AzureUploader.NewBlockBlobURL(url)

	_, err := azblob.UploadStreamToBlockBlob(ctx, reader, blob, azblob.UploadStreamToBlockBlobOptions{
		BufferSize: 4 * 1024 * 1024,
		MaxBuffers: 16,
	})
	if err != nil {
		return "", err
	}

	if config.Retain > 0 {
		deleteCtx := context.Background()

		res, err := s.AzureUploader.ListBlobsFlatSegment(deleteCtx, azblob.Marker{}, azblob.ListBlobsSegmentOptions{
			Prefix:     "raft_snapshot-",
			MaxResults: 500,
		})
		if err != nil {
			log.Errorln("Unable to iterate through bucket to find old snapshots to delete")
			return url, err
		}

		blobs := res.Segment.BlobItems
		timestamp := func(o1, o2 *azblob.BlobItemInternal) bool {
			return o1.Properties.LastModified.Before(o2.Properties.LastModified)
		}
		azureBy(timestamp).sort(blobs)

		if len(blobs)-int(config.Retain) <= 0 {
			return url, nil
		}

		blobsToDelete := blobs[0 : len(blobs)-int(config.Retain)]

		for _, b := range blobsToDelete {
			val := s.AzureUploader.NewBlockBlobURL(b.Name)

			_, err := val.Delete(deleteCtx, azblob.DeleteSnapshotsOptionInclude, azblob.BlobAccessConditions{})
			if err != nil {
				log.Errorln("Cannot delete old snapshot")
				return url, err
			}
		}
	}

	return url, nil
}

// implementation of Sort interface for s3 objects
type azureBy func(f1, f2 *azblob.BlobItemInternal) bool

func (by azureBy) sort(objects []azblob.BlobItemInternal) {
	fs := &azObjectSorter{
		objects: objects,
		by:      by, // The Sort method's receiver is the function (closure) that defines the sort order.
	}
	sort.Sort(fs)
}

type azObjectSorter struct {
	objects []azblob.BlobItemInternal
	by      func(f1, f2 *azblob.BlobItemInternal) bool // Closure used in the Less method.
}

func (s *azObjectSorter) Len() int {
	return len(s.objects)
}

func (s *azObjectSorter) Less(i, j int) bool {
	return s.by(&s.objects[i], &s.objects[j])
}

func (s *azObjectSorter) Swap(i, j int) {
	s.objects[i], s.objects[j] = s.objects[j], s.objects[i]
}
