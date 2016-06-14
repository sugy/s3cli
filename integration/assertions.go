package integration

import (
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/pivotal-golang/s3cli/client"
	"github.com/pivotal-golang/s3cli/config"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/s3"
	. "github.com/onsi/gomega"
)

// AssertLifecycleWorks tests the main blobstore object lifecycle from
// creation to deletion
func AssertLifecycleWorks(s3CLIPath string, cfg *config.S3Cli) {
	expectedString := GenerateRandomString()
	s3Filename := GenerateRandomString()

	configPath := MakeConfigFile(cfg)
	defer func() { _ = os.Remove(configPath) }()

	contentFile := MakeContentFile(expectedString)
	defer func() { _ = os.Remove(contentFile) }()

	s3CLISession, err := RunS3CLI(s3CLIPath, configPath, "put", contentFile, s3Filename)
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(BeZero())

	s3CLISession, err = RunS3CLI(s3CLIPath, configPath, "exists", s3Filename)
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(BeZero())
	Expect(s3CLISession.Err.Contents()).To(MatchRegexp("File '.*' exists in bucket '.*'"))

	tmpLocalFile, err := ioutil.TempFile("", "s3cli-download")
	Expect(err).ToNot(HaveOccurred())
	err = tmpLocalFile.Close()
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = os.Remove(tmpLocalFile.Name()) }()

	s3CLISession, err = RunS3CLI(s3CLIPath, configPath, "get", s3Filename, tmpLocalFile.Name())
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(BeZero())

	gottenBytes, err := ioutil.ReadFile(tmpLocalFile.Name())
	Expect(err).ToNot(HaveOccurred())
	Expect(string(gottenBytes)).To(Equal(expectedString))

	s3CLISession, err = RunS3CLI(s3CLIPath, configPath, "delete", s3Filename)
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(BeZero())

	s3CLISession, err = RunS3CLI(s3CLIPath, configPath, "exists", s3Filename)
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(Equal(3))
	Expect(s3CLISession.Err.Contents()).To(MatchRegexp("File '.*' does not exist in bucket '.*'"))
}

func AssertOnPutFailures(s3CLIPath string, cfg *config.S3Cli, errorMessage string) {
	expectedString := GenerateRandomString(1024 * 1024 * 6)
	s3Filename := GenerateRandomString()
	sourceContent := strings.NewReader(expectedString)

	configPath := MakeConfigFile(cfg)
	defer func() { _ = os.Remove(configPath) }()

	configFile, err := os.Open(configPath)
	Expect(err).ToNot(HaveOccurred())

	s3Config, err := config.NewFromReader(configFile)
	Expect(err).ToNot(HaveOccurred())

	s3Client, err := client.NewSDK(s3Config)
	if err != nil {
		log.Fatalln(err)
	}

	// Here we cause every other "part" to fail by way of SHA mismatch,
	// thereby triggering the MultiUploadFailure which causes retries.
	part := 0
	s3Client.Handlers.Build.PushBack(func(r *request.Request) {
		if r.Operation.Name == "UploadPart" {
			if part%2 == 0 {
				r.HTTPRequest.Header.Set("X-Amz-Content-Sha256", "000")
			}
			part++
		}
	})

	blobstoreClient, err := client.New(s3Client, &s3Config)
	Expect(err).ToNot(HaveOccurred())

	err = blobstoreClient.Put(sourceContent, s3Filename)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring(errorMessage))
}

// AssertPutOptionsApplied asserts that `s3cli put` uploads files with
// the requested encryption options
func AssertPutOptionsApplied(s3CLIPath string, cfg *config.S3Cli) {
	expectedString := GenerateRandomString()
	s3Filename := GenerateRandomString()

	configPath := MakeConfigFile(cfg)
	defer func() { _ = os.Remove(configPath) }()

	contentFile := MakeContentFile(expectedString)
	defer func() { _ = os.Remove(contentFile) }()

	configFile, err := os.Open(configPath)
	Expect(err).ToNot(HaveOccurred())

	s3CLISession, err := RunS3CLI(s3CLIPath, configPath, "put", contentFile, s3Filename)

	s3Config, err := config.NewFromReader(configFile)
	Expect(err).ToNot(HaveOccurred())

	client, _ := client.NewSDK(s3Config)
	resp, err := client.HeadObject(&s3.HeadObjectInput{
		Bucket: aws.String(cfg.BucketName),
		Key:    aws.String(s3Filename),
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(BeZero())

	if cfg.ServerSideEncryption == "" {
		Expect(resp.ServerSideEncryption).To(BeNil())
	} else {
		Expect(*resp.ServerSideEncryption).To(Equal(cfg.ServerSideEncryption))
	}
}

// AssertGetNonexistentFails asserts that `s3cli get` on a non-existent object
// will fail
func AssertGetNonexistentFails(s3CLIPath string, cfg *config.S3Cli) {
	configPath := MakeConfigFile(cfg)
	defer func() { _ = os.Remove(configPath) }()

	s3CLISession, err := RunS3CLI(s3CLIPath, configPath, "get", "non-existent-file", "/dev/null")
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).ToNot(BeZero())
	Expect(s3CLISession.Err.Contents()).To(ContainSubstring("NoSuchKey"))
}

// AssertDeleteNonexistentWorks asserts that `s3cli delete` on a non-existent
// object exits with status 0 (tests idempotency)
func AssertDeleteNonexistentWorks(s3CLIPath string, cfg *config.S3Cli) {
	configPath := MakeConfigFile(cfg)
	defer func() { _ = os.Remove(configPath) }()

	s3CLISession, err := RunS3CLI(s3CLIPath, configPath, "delete", "non-existent-file")
	Expect(err).ToNot(HaveOccurred())
	Expect(s3CLISession.ExitCode()).To(BeZero())
}
