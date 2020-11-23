package snapshot

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"

	"vault_raft_snapshotter/config"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	vaultApi "github.com/hashicorp/vault/api"
)

// Snapshotter primary struct
type Snapshotter struct {
	API             *vaultApi.Client
	Uploader        *s3manager.Uploader
	S3Client        *s3.S3
	GCPBucket       *storage.BucketHandle
	AzureUploader   azblob.ContainerURL
	TokenExpiration time.Time
}

// NewSnapshotter creates a new snaphotter instance
func NewSnapshotter(config *config.Configuration) (*Snapshotter, error) {
	snapshotter := &Snapshotter{}
	err := snapshotter.configureVaultClient(config)
	if err != nil {
		return nil, err
	}
	if config.AWS.Bucket != "" {
		err = snapshotter.configureS3(config)
		if err != nil {
			return nil, err
		}
	}
	if config.GCP.Bucket != "" {
		err = snapshotter.configureGCP(config)
		if err != nil {
			return nil, err
		}
	}
	if config.Azure.ContainerName != "" {
		err = snapshotter.configureAzure(config)
		if err != nil {
			return nil, err
		}
	}
	return snapshotter, nil
}

func (s *Snapshotter) configureVaultClient(config *config.Configuration) error {
	vaultConfig := vaultApi.DefaultConfig()
	if config.Vault.Address != "" {
		vaultConfig.Address = config.Vault.Address
	}
	tlsConfig := &vaultApi.TLSConfig{
		CACert:     config.Vault.CACert,
		ClientCert: config.Vault.ClientCert,
		ClientKey:  config.Vault.ClientKey,
		Insecure:   config.Vault.Insecure,
	}
	if err := vaultConfig.ConfigureTLS(tlsConfig); err != nil {
		return err
	}
	api, err := vaultApi.NewClient(vaultConfig)
	if err != nil {
		return err
	}
	s.API = api

	if config.Vault.RoleID != "" && config.Vault.SecretID != "" {
		if err := s.SetClientTokenFromAppRole(config); err != nil {
			return err
		}
	} else {
		if err := s.setClientTokenFromFile(config); err != nil {
			return err
		}
	}

	return nil
}

func (s *Snapshotter) setClientTokenFromFile(config *config.Configuration) error {
	t, err := ioutil.ReadFile(config.Vault.TokenFile)
	if err != nil {
		fmt.Print(err)
	}
	s.API.SetToken(string(t))
	s.TokenExpiration = time.Now().Add(time.Duration(time.Hour))
	return nil
}

// SetClientTokenFromAppRole sets the token via appRole login
func (s *Snapshotter) SetClientTokenFromAppRole(config *config.Configuration) error {
	data := map[string]interface{}{
		"role_id":   config.Vault.RoleID,
		"secret_id": config.Vault.SecretID,
	}
	resp, err := s.API.Logical().Write("auth/approle/login", data)
	if err != nil {
		return fmt.Errorf("error logging into AppRole auth backend: %s", err)
	}
	s.API.SetToken(resp.Auth.ClientToken)
	s.TokenExpiration = time.Now().Add(time.Duration((time.Second * time.Duration(resp.Auth.LeaseDuration)) / 2))
	return nil
}

func (s *Snapshotter) configureS3(config *config.Configuration) error {
	if config.AWS.Region == "" {
		config.AWS.Region = "us-east-1"
	}

	s3Transport := transportConfigS3(config)
	client := http.Client{Transport: s3Transport}

	awsConfig := &aws.Config{
		Region:     aws.String(config.AWS.Region),
		HTTPClient: &client,
	}


	if config.AWS.AccessKey != "" && config.AWS.SecretKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(config.AWS.AccessKey, config.AWS.SecretKey, "")
	}

	if config.AWS.Endpoint != "" {
		awsConfig.Endpoint = aws.String(config.AWS.Endpoint)
	}

	if config.AWS.S3ForcePathStyle {
		awsConfig.S3ForcePathStyle = aws.Bool(config.AWS.S3ForcePathStyle)
	}

	sess := session.Must(session.NewSession(awsConfig))
	s.S3Client = s3.New(sess)
	s.Uploader = s3manager.NewUploader(sess)
	return nil
}

func (s *Snapshotter) configureGCP(config *config.Configuration) error {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return err
	}
	s.GCPBucket = client.Bucket(config.GCP.Bucket)
	return nil
}

func (s *Snapshotter) configureAzure(config *config.Configuration) error {
	accountName := config.Azure.AccountName
	if os.Getenv("AZURE_STORAGE_ACCOUNT") != "" {
		accountName = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	accountKey := config.Azure.AccountKey
	if os.Getenv("AZURE_STORAGE_ACCESS_KEY") != "" {
		accountKey = os.Getenv("AZURE_STORAGE_ACCESS_KEY")
	}
	if len(accountName) == 0 || len(accountKey) == 0 {
		return errors.New("Invalid Azure configuration")
	}
	credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		log.Fatal("Invalid credentials with error: " + err.Error())
	}
	p := azblob.NewPipeline(credential, azblob.PipelineOptions{})
	URL, _ := url.Parse(
		fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, config.Azure.ContainerName))

	s.AzureUploader = azblob.NewContainerURL(*URL, p)
	return nil
}

func transportConfigS3(config *config.Configuration) http.RoundTripper {
	cfg := new(tls.Config)

	// Enforce TLS1.2 or higher
	cfg.MinVersion = 2

	// Read system CAs
	var systemCAs, _ = x509.SystemCertPool()
	if reflect.DeepEqual(systemCAs, x509.NewCertPool()) {
		log.Debug("creating new CApool")
		systemCAs = x509.NewCertPool()
	}
	cfg.RootCAs = systemCAs

	if config.AWS.CACert != "" {
		cacert, e := ioutil.ReadFile(config.AWS.CACert) // #nosec this file comes from our config
		if e != nil {
			log.Fatalf("failed to append %q to RootCAs: %v", cacert, e)
		}
		if ok := cfg.RootCAs.AppendCertsFromPEM(cacert); !ok {
			log.Debug("no certs appended, using system certs only")
		}
	}

	var trConfig http.RoundTripper = &http.Transport{
		TLSClientConfig:   cfg,
		ForceAttemptHTTP2: true}

	return trConfig
}
