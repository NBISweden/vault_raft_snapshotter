package main

import (
	"bytes"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/Lucretius/vault_raft_snapshot_agent/config"
	"github.com/Lucretius/vault_raft_snapshot_agent/snapshot"
)

func listenForInterruptSignals() chan bool {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan bool, 1)

	go func() {
		_ = <-sigs
		done <- true
	}()
	return done
}

func main() {
	done := listenForInterruptSignals()

	log.Infoln("Reading configuration...")
	c, err := config.ReadConfig()

	if err != nil {
		log.Fatalln("Configuration could not be found")
	}

	snapshotter, err := snapshot.NewSnapshotter(c)
	frequency, err := time.ParseDuration(c.Frequency)

	if err != nil {
		frequency = time.Hour
	}

	if err != nil {
		log.Fatalln("Error retrieving Current instance IP.  Verify internet connectivity.")
	}
	for {
		if snapshotter.TokenExpiration.Before(time.Now()) && (c.Vault.RoleID != "" && c.Vault.SecretID != "") {
			snapshotter.SetClientTokenFromAppRole(c)
		}
		leader, err := snapshotter.API.Sys().Leader()
		if err != nil {
			log.Errorln(err.Error())
			log.Fatalln("Unable to determine leader instance.  The snapshot agent will only run on the leader node.  Are you running this daemon on a Vault instance?")
		}

		if !leader.IsSelf {
			log.Infoln("Not running on leader node, skipping.")
		} else {
			var snapshot bytes.Buffer
			err := snapshotter.API.Sys().RaftSnapshot(&snapshot)
			if err != nil {
				log.Fatalln("Unable to generate snapshot", err.Error())
			}
			now := time.Now().UnixNano()
			if c.Local.Path != "" {
				snapshotPath, err := snapshotter.CreateLocalSnapshot(&snapshot, c, now)
				logSnapshotError("local", snapshotPath, err)
			}
			if c.AWS.Bucket != "" {
				snapshotPath, err := snapshotter.CreateS3Snapshot(&snapshot, c, now)
				logSnapshotError("aws", snapshotPath, err)
			}
			if c.GCP.Bucket != "" {
				snapshotPath, err := snapshotter.CreateGCPSnapshot(&snapshot, c, now)
				logSnapshotError("gcp", snapshotPath, err)
			}
			if c.Azure.ContainerName != "" {
				snapshotPath, err := snapshotter.CreateAzureSnapshot(&snapshot, c, now)
				logSnapshotError("azure", snapshotPath, err)
			}
		}
		select {
		case <-time.After(frequency):
			continue
		case <-done:
			os.Exit(1)
		}
	}
}

func logSnapshotError(dest, snapshotPath string, err error) {
	if err != nil {
		log.Errorf("Failed to generate %s snapshot to %s: %v\n", dest, snapshotPath, err)
	} else {
		log.Infof("Successfully created %s snapshot to %s\n", dest, snapshotPath)
	}
}
