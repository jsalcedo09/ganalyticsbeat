# Ganalyticsbeat

Welcome to Ganalyticsbeat.

Ensure that this folder is at the following location:
`${GOPATH}/github.com/jsalcedo09`

## Getting Started with Ganalyticsbeat

### Thanks

Special thanks to [Cloudflare Beat](https://github.com/hartfordfive/cloudflarebeat) that worked as inspiration.

### Requirements

* [Golang](https://golang.org/dl/) 1.7

### Google Analytics Beat specific configuration options

- `ganalyticsbeat.period` : The period at which the google analytics metrics will be fetched.  
- `ganalyticsbeat.view_id` :(mandatory) The View ID of the Google Analytics View 
- `ganalyticsbeat.initial_date` : (mandatory) The starting date for Google Analytics Data in format `YYYY-MM-DD`
- `ganalyticsbeat.metrics` : (mandatory) The metrics list in comma separated values  `ga:sessions,ga:users,ga:pageviews,ga:bounceRate` 
- `ganalyticsbeat.dimensions` : The dimensions list in comma separated values  `ga:eventCategory, ga:eventLabel`
- `ganalyticsbeat.view_timezone` : (mandatory) The timezone which the Google Analytics View is configured `America/Bogota`
- `ganalyticsbeat.state_file_storage_type` : The type of storage for the state file, either `disk` or `s3`, which keeps track of the current progress. (Default: disk)
- `ganalyticsbeat.state_file_path` : The path in which the state file will be saved (applicable only with `disk` storage type)
- `ganalyticsbeat.state_file_name` : The name of the state file
- `ganalyticsbeat.aws_access_key` : The user AWS access key, if S3 storage selected.
- `ganalyticsbeat.aws_secret_access_key` : The user AWS secret access key, if S3 storage selected.
- `ganalyticsbeat.aws_s3_bucket_name` : The name of the S3 bucket where the state file will be stored
- `ganalyticsbeat.google_credentials` : JSON as a string with Google Credentials tokens
- `ganalyticsbeat.google_secrets` : JSON as a string with Google Secrets on the [Google API Console](https://console.developers.google.com/)

- `ganalyticsbeat.google_auth_flow` : Special flag to start the auth flow to get credentials JSON. Defaults to `false`

### Using S3 Storage for state file

For ganalyticsbeat, it's probably best to create a seperate IAM user account, without a password and only this sample policy file.  Best to limit the access of your user as a security practice.

Below is a sample of what the policy file would look like for the S3 storage.  Please note you should replace `my-ganalyticsbeat-bucket-name` with your bucket name that you've created in S3.

```
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "s3:ListBucket"
            ],
            "Resource": [
                "arn:aws:s3:::my-ganalyticsbeat-bucket-name"
            ]
        },
        {
            "Effect": "Allow",
            "Action": [
                "s3:PutObject",
                "s3:GetObject",
                "s3:DeleteObject"
            ],
            "Resource": [
                "arn:aws:s3:::my-ganalyticsbeat-bucket-name/*"
            ]
        }
    ]
}
```

### Init Project
To get running with Ganalyticsbeat and also install the
dependencies, run the following command:

```
make setup
```

It will create a clean git history for each major step. Note that you can always rewrite the history if you wish before pushing your changes.

To push Ganalyticsbeat in the git repository, run the following commands:

```
git remote set-url origin https://github.com/jsalcedo09/ganalyticsbeat
git push origin master
```

For further development, check out the [beat developer guide](https://www.elastic.co/guide/en/beats/libbeat/current/new-beat.html).

### Build

To build the binary for Ganalyticsbeat run the command below. This will generate a binary
in the same directory with the name ganalyticsbeat.

```
make
```


### Run

To run Ganalyticsbeat with debugging output enabled, run:

```
./ganalyticsbeat -c ganalyticsbeat.yml -e -d "*"
```


### Test

To test Ganalyticsbeat, run the following command:

```
make testsuite
```

alternatively:
```
make unit-tests
make system-tests
make integration-tests
make coverage-report
```

The test coverage is reported in the folder `./build/coverage/`

### Update

Each beat has a template for the mapping in elasticsearch and a documentation for the fields
which is automatically generated based on `etc/fields.yml`.
To generate etc/ganalyticsbeat.template.json and etc/ganalyticsbeat.asciidoc

```
make update
```


### Cleanup

To clean  Ganalyticsbeat source code, run the following commands:

```
make fmt
make simplify
```

To clean up the build directory and generated artifacts, run:

```
make clean
```


### Clone

To clone Ganalyticsbeat from the git repository, run the following commands:

```
mkdir -p ${GOPATH}/github.com/jsalcedo09
cd ${GOPATH}/github.com/jsalcedo09
git clone https://github.com/jsalcedo09/ganalyticsbeat
```


For further development, check out the [beat developer guide](https://www.elastic.co/guide/en/beats/libbeat/current/new-beat.html).


## Packaging

The beat frameworks provides tools to crosscompile and package your beat for different platforms. This requires [docker](https://www.docker.com/) and vendoring as described above. To build packages of your beat, run the following command:

```
make package
```

This will fetch and create all images required for the build process. The hole process to finish can take several minutes.
