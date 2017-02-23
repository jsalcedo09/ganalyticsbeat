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
	b64 "encoding/base64"
	"errors"
)

const (
	STATEFILE_NAME = "ganalyticsbeat.state"
	OFFSET_PAST_MINUTES = 30
	MAX_RETRIES = 30
)

type Ganalyticsbeat struct {
	done    chan struct{}
	config  config.Config
	client  publisher.Client
	state   *ganalytics.StateFile
	gclient *http.Client
	beat *beat.Beat
}

var timeStart, timeEnd, timeNow, fails, dropoff, totalevents int
var publishing bool

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
		beat: b,
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

	gclient := bt.getClient(ctx, oauthconfig, bt.config.GoogleAuthFlow)
	bt.gclient = gclient

	return bt, nil
}

func (bt *Ganalyticsbeat) Run(b *beat.Beat) error {
	logp.Info("ganalyticsbeat is running! Hit CTRL-C to stop it.")
	if bt.config.GoogleAuthFlow {
		logp.Warn("Auth mode [google_auth_flow] enabled in config file, disable it to run beat")
		return nil
	}
	bt.client = b.Publisher.Connect()

	if bt.state.GetLastEndTS() != 0 {
		timeNow = int(time.Now().UTC().Unix())
		timeDiff := int(timeNow - (bt.state.GetLastEndTS() + 1))
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
			timeEnd := timeNow
			logp.Info("Catching up. Immediately processing logs between %s to %s", time.Unix(int64(timeStart), 0), time.Unix(int64(timeEnd), 0))
			bt.DownloadAndPublish(int(time.Now().UTC().Unix()), timeStart, timeEnd)
		}
	}

	ticker := time.NewTicker(bt.config.Period)
	fails = 0
	dropoff = 0
	publishing = false
	for {
		select {
		case <-bt.done:
			return nil
		case <-ticker.C:
		}
		if dropoff == 0 {
			if !publishing {
				publishing = true
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
				timeEnd := timeNow
				err := bt.DownloadAndPublish(timeNow, timeStart, timeEnd)
				if err != nil {
					fails += 1
					if dropoff < MAX_RETRIES {
						dropoff += fails
					} else {
						dropoff = MAX_RETRIES
					}
					logp.Warn("Retrying in %s because of the error", time.Duration(bt.config.Period.Seconds() * float64(dropoff + 1)) * time.Second)
				} else {
					fails = 0
					dropoff = 0
				}
				publishing = false
			}else{
				logp.Warn("Already publishing, wating for next tick...")
			}
		}else{
			logp.Warn("%s to next run", time.Duration(bt.config.Period.Seconds() * float64(dropoff))*time.Second )
			dropoff -= 1
		}
	}
}

func (bt *Ganalyticsbeat) startAuthFlow() error {

	return nil
}

func (bt *Ganalyticsbeat) DownloadAndPublish(timeNow int, timeStart int, timeEnd int) error {
	bt.state.UpdateLastRequestTS(timeNow)
	loc, err := time.LoadLocation(bt.config.ViewTimeZone)
	if err != nil {
		logp.Err("Error loading timezone %v", err)
	}
	tms := time.Unix(int64(timeStart), 0)
	tms = tms.In(loc)
	tme := time.Unix(int64(timeEnd), 0)
	tme = tme.In(loc)
	logp.Info("Fetching report from %s to %s", tms.Format("2006-01-02 15:04 -0700"), tme.Format("2006-01-02 15:04 -0700"))
	requests := &analyticsreporting.GetReportsRequest{}
	requests.ReportRequests = []*analyticsreporting.ReportRequest{
		{
			ViewId:bt.config.ViewId,
			SamplingLevel:"LARGE",
			PageSize:bt.config.BatchSize,
			Dimensions:[]*analyticsreporting.Dimension{
				{
					Name:"ga:date",
				},
				{
					Name:"ga:hour",
				},
			},
			DateRanges:[]*analyticsreporting.DateRange{
				{
					StartDate:tms.Format("2006-01-02"),
					EndDate:tme.Format("2006-01-02"),
				},
			},
		},
	}

	if bt.config.Dimensions != "" {
		for _, dimension := range strings.Split(bt.config.Dimensions, ",") {
			requests.ReportRequests[0].Dimensions = append(requests.ReportRequests[0].Dimensions,
				&analyticsreporting.Dimension{
					Name:strings.Trim(dimension, " "),
				},
			)
		}
	}

	if bt.config.Metrics != "" {
		for _, metric := range strings.Split(bt.config.Metrics, ",") {
			requests.ReportRequests[0].Metrics = append(requests.ReportRequests[0].Metrics,
				&analyticsreporting.Metric{
					Expression:strings.Trim(metric, " "),
				},
			)
		}
	}
	totalevents = 0
	err = bt.fetchRequest(requests, "0")
	if  err != nil {
		logp.Err("Unable to retrieve report: %v", err)
		return err

	}else{
		bt.state.UpdateLastStartTS(timeStart)
		bt.state.UpdateLastRequestTS(timeNow)

		if err := bt.state.Save(); err != nil {
			logp.Info("[ERROR] Could not persist state file to storage: %s", err.Error())
		} else {
			logp.Info("Updated state file")
		}
		return nil
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
		return err
	}
	for i, report := range r.Reports {
		bt.processReport(report)
		if report.NextPageToken != "" {
			token, err := strconv.ParseInt(report.NextPageToken, 0, 64)
			if err != nil {
				return err
			}
			if token > 0 {
				logp.Info("Fetching next page report %s", report.NextPageToken)
				err = bt.fetchRequest(requests, report.NextPageToken)
				if err != nil {
					return err
				}
			}
		}
		var tot int
		for _, val := range report.Data.Totals[i].Values {
			num, _ := strconv.ParseInt(val, 10, 64)
			tot += int(num)
		}
		logp.Info("Published %s of %s events", totalevents, int(report.Data.RowCount)*len(report.ColumnHeader.MetricHeader.MetricHeaderEntries))
	}

	return nil

}

func (bt *Ganalyticsbeat) processReport(report *analyticsreporting.Report) error {
	headers := bt.getReportHeaders(report)
	dimensions := report.ColumnHeader.Dimensions
	logp.Info("Fetching %s records", report.Data.RowCount)
	var cont int
	cont = 0

	for _, row := range report.Data.Rows {
		ts := bt.getRowTimestamp(dimensions, row)
		err := bt.processRow(ts, headers, dimensions, row)
		if err != nil{
			logp.Warn("Error processing row %v", err)
		}else{
			bt.state.UpdateLastEndTS(int(ts.Unix()))
			cont++
		}
	}
	logp.Info("Processed %s rows", cont)
	return nil
}

func (bt *Ganalyticsbeat) processRow(ts time.Time, headers *analyticsreporting.MetricHeader, dimensions []string, row *analyticsreporting.ReportRow) error {
	var cont int
	var events []common.MapStr
	cont = 0

	for _, metric := range row.Metrics {
			for i, value := range metric.Values {
				key := bt.getRowKey(headers.MetricHeaderEntries[i].Name, dimensions, row, ts)
				if bt.state.GetLastKeyEndTS(key) < int(ts.Unix()){
					switch headers.MetricHeaderEntries[i].Type {
					case "INTEGER", "PERCENT":
						val, _ := strconv.ParseFloat(value, 64)
						event := common.MapStr{
							"@timestamp": common.Time(ts),
							"type":       headers.MetricHeaderEntries[i].Name,
							"value":      val,
							"key": key,
						}
						event = bt.buildDimensions(event, dimensions, row)
						events = append(events, event)
						bt.client.PublishEvent(event)
						cont++
						totalevents += 1
					default:
						event := common.MapStr{
							"@timestamp": common.Time(ts),
							"type":       headers.MetricHeaderEntries[i].Name,
							"value":      value,
							"key": key,
						}
						event = bt.buildDimensions(event, dimensions, row)
						events = append(events, event)
						bt.client.PublishEvent(event)
						cont++
						totalevents += 1

					}
					bt.state.UpdateLastKeyEndTS(key, int(ts.Unix()))
				}else{
					return errors.New("Row already proceced")
				}
			}

	}

	return nil
}

func (bt *Ganalyticsbeat) getRowKey(header string, dimensions []string, row *analyticsreporting.ReportRow, ts time.Time) string {
	key, _ := bt.beat.Config.Output["elasticsearch"].String("index",0)
	key = fmt.Sprintf("%s_%s",key, header)
	for i, dim := range dimensions{
		switch dim {
			case "ga:date", "ga:hour", "ga:minute":
				continue
			default:
				formated_dim := strings.Replace(dim, ":", ".", -1)
				key = fmt.Sprintf("%s_%s:%s",key, formated_dim, row.Dimensions[i])
		}
	}
	return b64.StdEncoding.EncodeToString([]byte(key))
}

func (bt *Ganalyticsbeat) getReportHeaders(report *analyticsreporting.Report) *analyticsreporting.MetricHeader {
	return report.ColumnHeader.MetricHeader
}

func (bt *Ganalyticsbeat) buildDimensions(event common.MapStr, dimensions []string, row *analyticsreporting.ReportRow) common.MapStr {
	for i, dim := range dimensions{
		switch dim {
			case "ga:date", "ga:hour", "ga:minute":
				continue
			default:
				formated_dim := strings.Replace(dim, ":", ".", -1)
				event.Put(formated_dim, row.Dimensions[i])
		}
	}
	return event
}

func (bt *Ganalyticsbeat) getRowTimestamp(dimensions []string, row *analyticsreporting.ReportRow) time.Time {
	var date_index int
	var hour_index int
	loc, err := time.LoadLocation(bt.config.ViewTimeZone)
	if err != nil {
		logp.Err("Error loading timezone %v", err)
	}
	for i, dim := range dimensions {
		switch dim {
			case "ga:date":
				date_index = i
			case "ga:hour":
				hour_index = i
		}
	}

	ts, err := time.Parse("20060102 15:04 -0700", fmt.Sprintf("%s %s:%s %s",
		row.Dimensions[date_index],
		row.Dimensions[hour_index],
		"00", //Minute 00
		time.Now().In(loc).Format("-0700")))
	if err != nil {
		logp.Err("Error parsing row date %v", err)
	}
	return ts
}

// getClient uses a Context and Config to retrieve a Token
// then generate a Client. It returns the generated Client.
func (bt *Ganalyticsbeat) getClient(ctx context.Context, config *oauth2.Config, auth bool) *http.Client {
	cacheFile, err := bt.tokenCacheFile()
	if err != nil {
		logp.Err("Unable to get path to cached credential file. %v", err)
	}

	if auth {
		tok := bt.getTokenFromWeb(config)
		jsonString, _ := json.Marshal(tok)
		logp.Info("Token: %s", jsonString)
		return config.Client(ctx, tok)
	}else {
		tok, err := bt.tokenFromFile(cacheFile)
		if err != nil {
			tok = bt.getTokenFromConfig()
			bt.saveToken(cacheFile, tok)
		}
		return config.Client(ctx, tok)
	}
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


