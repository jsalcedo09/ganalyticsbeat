// Config is put into a different package to prevent cyclic imports in case
// it is needed in several locations

package config

import "time"

type Config struct {
	Period 			  time.Duration    `config:"period"`
	StateFileStorageType      string           `config:"state_file_storage_type"`
	StateFileName             string           `config:"state_file_name"`
	StateFilePath             string           `config:"state_file_path"`
	AwsAccessKey              string           `config:"aws_access_key"`
	AwsSecretAccessKey        string           `config:"aws_secret_access_key"`
	AwsS3BucketName           string           `config:"aws_s3_bucket_name"`
	InitialDate               string           `config:"initial_date"`
	ViewId                    string           `config:"view_id"`
	Metrics                   string           `config:"metrics"`
	GoogleSecrets		  string	   `config:"google_secrets"`
	GoogleCredentials         string           `config:"google_credentials"`


}

var DefaultConfig = Config{
	Period: 		      10 * time.Minute,
	StateFileStorageType:         "disk",
	StateFileName:                "ganalyticsbeat",
	StateFilePath:                "/etc/ganalyticsbeat/",
}
