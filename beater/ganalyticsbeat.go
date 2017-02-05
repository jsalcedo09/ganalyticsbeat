package beater

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/publisher"
	"github.com/jsalcedo09/ganalyticsbeat/ganalytics"
	"github.com/jsalcedo09/ganalyticsbeat/config"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/analyticsreporting/v4"

	"time"
	"strconv"
	"strings"
)

const (
	STATEFILE_NAME = "ganalyticsbeat.state"
	OFFSET_PAST_MINUTES = 30
)

type Ganalyticsbeat struct {
	done    chan struct{}
	config  config.Config
	client  publisher.Client
	state   *ganalytics.StateFile
	gclient *http.Client
}

var timeStart, timeEnd, timeNow int

// Creates beater
func New(b *beat.Beat, cfg *common.Config) (beat.Beater, error) {
	config := config.DefaultConfig
	if err := cfg.Unpack(&config); err != nil {
		return nil, fmt.Errorf("Error reading config file: %v", err)
	}

	if config.Period.Minutes() < 1 || config.Period.Minutes() > 30 {
		logp.Warn("Chosen period of %s is not valid. Changing to 5m", config.Period.String())
		config.Period = 5 * time.Minute
	}

	bt := &Ganalyticsbeat{
		done: make(chan struct{}),
		config: config,
	}

	sfConf := map[string]string{
		"filename":     config.StateFileName,
		"filepath":     config.StateFilePath,
		"storage_type": config.StateFileStorageType,
	}

	if config.AwsAccessKey != "" && config.AwsSecretAccessKey != "" && config.AwsS3BucketName != "" {
		sfConf["aws_access_key"] = config.AwsAccessKey
		sfConf["aws_secret_access_key"] = config.AwsSecretAccessKey
		sfConf["aws_s3_bucket_name"] = config.AwsS3BucketName
	}

	sf, err := ganalytics.NewStateFile(sfConf)

	if err != nil {
		logp.Err("Statefile error: %v", err)
		return nil, err
	}
	bt.state = sf
	ctx := context.Background()
	csf := []byte(bt.config.GoogleSecrets)
	oauthconfig, err := google.ConfigFromJSON(csf, analyticsreporting.AnalyticsReadonlyScope)
	if err != nil {
		logp.Err("Unable to parse client secret file to config: %v", err)
	}

	gclient := bt.getClient(ctx, oauthconfig)
	bt.gclient = gclient

	return bt, nil
}

func (bt *Ganalyticsbeat) Run(b *beat.Beat) error {
	logp.Info("ganalyticsbeat is running! Hit CTRL-C to stop it. %s", bt.state.GetLastEndTS())
	bt.client = b.Publisher.Connect()

	if bt.state.GetLastEndTS() != 0 {
		timeNow = int(time.Now().UTC().Unix())
		timeDiff := int((timeNow - (OFFSET_PAST_MINUTES * 60)) - (bt.state.GetLastEndTS() + 1))
		timeStart = bt.state.GetLastEndTS() + 1

		// If the time difference from NOW to the last time the DownloadAndPublish ran is greater than the configured period,
		// then sleep for the resulting delta, then download and process the logs for the period
		if timeDiff < int(bt.config.Period.Seconds()) {
			timeEnd := timeStart + int(bt.config.Period.Seconds())
			timeWait := int(bt.config.Period.Seconds()) - timeDiff
			logp.Info("Waiting for %d seconds before catching up", timeWait)
			time.Sleep(time.Duration(timeWait) * time.Second)
			logp.Info("Catching up. Processing logs between %s to %s", time.Unix(int64(timeStart), 0), time.Unix(int64(timeEnd), 0))
			bt.DownloadAndPublish(int(time.Now().UTC().Unix()), timeStart, timeEnd)
		} else {
			// In this case, the time difference from NOW to the last time the DownloadAndPublish ran is greater than
			// the configured period, so run immediately before starting the ticker
			timeEnd := timeNow - (OFFSET_PAST_MINUTES * 60)
			logp.Info("Catching up. Immediately processing logs between %s to %s", time.Unix(int64(timeStart), 0), time.Unix(int64(timeEnd), 0))
			bt.DownloadAndPublish(int(time.Now().UTC().Unix()), timeStart, timeEnd)
		}
	}

	ticker := time.NewTicker(bt.config.Period)
	for {
		select {
		case <-bt.done:
			return nil
		case <-ticker.C:
		}

		timeNow = int(time.Now().UTC().Unix())

		if bt.state.GetLastStartTS() != 0 {
			timeStart = bt.state.GetLastEndTS() + 1 // last end TS as per statefile + 1 second
		} else {
			t2, err := time.Parse("2006-01-02", bt.config.InitialDate)
			if err != nil {
				logp.Err("Error parsing Initial Date from config %v", err)
			}
			timeStart = int(t2.Unix())
		}
		timeEnd := timeNow - (OFFSET_PAST_MINUTES * 60)
		bt.DownloadAndPublish(timeNow, timeStart, timeEnd)
	}
}

func (bt *Ganalyticsbeat) DownloadAndPublish(timeNow int, timeStart int, timeEnd int) {
	bt.state.UpdateLastRequestTS(timeNow)
	tms := time.Unix(int64(timeStart), 0)
	tme := time.Unix(int64(timeEnd), 0)
	logp.Info("Fetching report from %s to %s", tms.Format("2006-01-02 15:04"), tme.Format("2006-01-02 15:04"))
	requests := &analyticsreporting.GetReportsRequest{}
	requests.ReportRequests = []*analyticsreporting.ReportRequest{
		{
			ViewId:bt.config.ViewId,
			SamplingLevel:"LARGE",
			Dimensions:[]*analyticsreporting.Dimension{
				{
					Name:"ga:date",
				},
				{
					Name:"ga:hour",
				},
				{
					Name:"ga:minute",
				},
			},
			DateRanges:[]*analyticsreporting.DateRange{
				{
					StartDate:tms.Format("2006-01-02"),
					EndDate:tme.Format("2006-01-02"),
				},
			},
			DimensionFilterClauses:[]*analyticsreporting.DimensionFilterClause{
				{
					Filters:[]*analyticsreporting.DimensionFilter{
						{
							Operator:"NUMERIC_GREATER_THAN",
							DimensionName:"ga:date",
							Expressions:[]string{tms.Format("20060102")},
						},
						{
							Operator:"NUMERIC_GREATER_THAN",
							DimensionName:"ga:hour",
							Expressions:[]string{tms.Format("15")},
						},
						{
							Operator:"NUMERIC_GREATER_THAN",
							DimensionName:"ga:minute",
							Expressions:[]string{tms.Format("04")},
						},
					},
				},
			},
		},
	}
	for _, metric := range strings.Split(bt.config.Metrics, ",") {
		requests.ReportRequests[0].Metrics = append(requests.ReportRequests[0].Metrics,
			&analyticsreporting.Metric{
				Expression:strings.Trim(metric, " "),
			},
		)
	}

	bt.fetchRequest(requests, "0")

	bt.state.UpdateLastStartTS(timeStart)
	bt.state.UpdateLastRequestTS(timeNow)

	if err := bt.state.Save(); err != nil {
		logp.Info("[ERROR] Could not persist state file to storage: %s", err.Error())
	} else {
		logp.Info("Updated state file")
	}
}

func (bt *Ganalyticsbeat) fetchRequest(requests *analyticsreporting.GetReportsRequest, pageToken string) error {
	requests.ReportRequests[0].PageToken = pageToken
	srv, err := analyticsreporting.New(bt.gclient)
	if err != nil {
		logp.Err("Unable to retrieve $GOPATH Client %v", err)
	}

	r, err := srv.Reports.BatchGet(requests).Do()
	if err != nil {
		logp.Err("Unable to retrieve report: %v", err)
	}
	for _, report := range r.Reports {
		bt.processReport(report)
		if report.NextPageToken != "" {
			token, err := strconv.ParseInt(report.NextPageToken, 0, 64)
			if err != nil {
				logp.Err("Error parsing NetxPageToken: %v", err)
			}
			if token > 0 {
				logp.Info("Fetching next page report %s", report.NextPageToken)
				bt.fetchRequest(requests, report.NextPageToken)
			}
		}
	}

	return nil

}

func (bt *Ganalyticsbeat) processReport(report *analyticsreporting.Report) error {
	headers := bt.getReportHeaders(report)
	for _, row := range report.Data.Rows {
		ts := bt.getRowTimestamp(row)
		if bt.state.GetLastEndTS() < int(ts.Unix()) {
			bt.processRow(ts, headers, row)
		}
	}

	return nil
}

func (bt *Ganalyticsbeat) processRow(ts time.Time, headers *analyticsreporting.MetricHeader, row *analyticsreporting.ReportRow) error {
	for _, metric := range row.Metrics {
		for i, value := range metric.Values {
			switch headers.MetricHeaderEntries[i].Type {
			case "INTEGER", "PERCENT":
				val, _ := strconv.ParseFloat(value, 64)
				event := common.MapStr{
					"@timestamp": common.Time(ts),
					"type":       headers.MetricHeaderEntries[i].Name,
					"value":      val,
				}
				bt.client.PublishEvent(event)
				logp.Info("Event sent")
			default:
				event := common.MapStr{
					"@timestamp": common.Time(ts),
					"type":       headers.MetricHeaderEntries[i].Name,
					"value":      value,
				}
				bt.client.PublishEvent(event)
				logp.Info("Event sent")

			}
		}
	}
	bt.state.UpdateLastEndTS(int(ts.Unix()))
	return nil
}

func (bt *Ganalyticsbeat) getReportHeaders(report *analyticsreporting.Report) *analyticsreporting.MetricHeader {
	return report.ColumnHeader.MetricHeader
}

func (bt *Ganalyticsbeat) getRowTimestamp(row *analyticsreporting.ReportRow) time.Time {
	ts, err := time.Parse("20060102 15:04", fmt.Sprintf("%s %s:%s", row.Dimensions[0], row.Dimensions[1], row.Dimensions[2]))
	if err != nil {
		logp.Err("Error parsing row date %v", err)
	}
	return ts
}

// getClient uses a Context and Config to retrieve a Token
// then generate a Client. It returns the generated Client.
func (bt *Ganalyticsbeat) getClient(ctx context.Context, config *oauth2.Config) *http.Client {
	cacheFile, err := bt.tokenCacheFile()
	if err != nil {
		logp.Err("Unable to get path to cached credential file. %v", err)
	}
	tok, err := bt.tokenFromFile(cacheFile)
	if err != nil {
		tok = bt.getTokenFromConfig()
		bt.saveToken(cacheFile, tok)
	}
	return config.Client(ctx, tok)
}

// getTokenFromWeb uses Config to request a Token.
// It returns the retrieved Token.
func (bt *Ganalyticsbeat) getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the " +
		"authorization code: \n%v\n", authURL)

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		logp.Err("Unable to read authorization code %v", err)
	}

	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		logp.Err("Unable to retrieve token from web %v", err)
	}
	return tok
}

func (bt *Ganalyticsbeat) getTokenFromConfig() *oauth2.Token {
	logp.Info("Parsing token from beat configuration")
	tok := &oauth2.Token{}
	byt := []byte(bt.config.GoogleCredentials)
	err := json.Unmarshal(byt, &tok)
	if err != nil {
		logp.Err("Unable to parse token from config %v", err)
	}
	return tok
}


// tokenCacheFile generates credential file path/filename.
// It returns the generated credential path/filename.
func (bt *Ganalyticsbeat) tokenCacheFile() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	tokenCacheDir := filepath.Join(usr.HomeDir, ".credentials")
	os.MkdirAll(tokenCacheDir, 0700)
	return filepath.Join(tokenCacheDir,
		url.QueryEscape("analyticsreporting-go-quickstart.json")), err
}

// tokenFromFile retrieves a Token from a given file path.
// It returns the retrieved Token and any read error encountered.
func (bt *Ganalyticsbeat) tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	t := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(t)
	defer f.Close()
	return t, err
}

// saveToken uses a file path to create a file and store the
// token in it.
func (bt *Ganalyticsbeat) saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.Create(file)
	if err != nil {
		logp.Err("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func (bt *Ganalyticsbeat) Stop() {
	if err := bt.state.Save(); err != nil {
		logp.Info("[ERROR] Could not persist state file to storage while shutting down: %s", err.Error())
	}
	bt.client.Close()
	close(bt.done)
}


