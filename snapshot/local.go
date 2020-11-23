package snapshot

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"vault_raft_snapshotter/config"
)

// CreateLocalSnapshot writes snapshot to disk location
func (s *Snapshotter) CreateLocalSnapshot(buf *bytes.Buffer, config *config.Configuration, currentTs int64) (string, error) {
	fileName := fmt.Sprintf("%s/raft_snapshot-%d.snap", config.Local.Path, currentTs)

	err := ioutil.WriteFile(fileName, buf.Bytes(), 0644) //nolint:gosec
	if err != nil {
		return "", err
	}

	if config.Retain > 0 {
		fileInfo, err := ioutil.ReadDir(config.Local.Path)
		if err != nil {
			log.Errorln("Unable to read file directory to delete old snapshots")
			return fileName, err
		}

		filesToDelete := make([]os.FileInfo, 0)
		
		for _, file := range fileInfo {
			if strings.Contains(file.Name(), "raft_snapshot-") && strings.HasSuffix(file.Name(), ".snap") {
				filesToDelete = append(filesToDelete, file)
			}
		}

		timestamp := func(f1, f2 *os.FileInfo) bool {
			file1 := *f1
			file2 := *f2

			return file1.ModTime().Before(file2.ModTime())
		}

		fileBy(timestamp).sort(filesToDelete)

		if len(filesToDelete) <= int(config.Retain) {
			return fileName, nil
		}

		filesToDelete = filesToDelete[0 : len(filesToDelete)-int(config.Retain)]

		for _, f := range filesToDelete {
			log.Debugf("Deleting old snapshot %s", f.Name())
			os.Remove(fmt.Sprintf("%s/%s", config.Local.Path, f.Name()))
		}
	}

	return fileName, nil
}

// implements a Sort interface for fileInfo
type fileBy func(f1, f2 *os.FileInfo) bool

func (by fileBy) sort(files []os.FileInfo) {
	fs := &fileSorter{
		files: files,
		by:    by, // The Sort method's receiver is the function (closure) that defines the sort order.
	}
	sort.Sort(fs)
}

type fileSorter struct {
	files []os.FileInfo
	by    func(f1, f2 *os.FileInfo) bool // Closure used in the Less method.
}

func (s *fileSorter) Len() int {
	return len(s.files)
}

func (s *fileSorter) Less(i, j int) bool {
	return s.by(&s.files[i], &s.files[j])
}

func (s *fileSorter) Swap(i, j int) {
	s.files[i], s.files[j] = s.files[j], s.files[i]
}
