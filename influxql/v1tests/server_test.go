package v1tests

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/cmd/influxd/launcher"
	"github.com/influxdata/influxdb/v2/models"
	"github.com/stretchr/testify/require"
)

const (
	NeedsReview  = "Needs review"   // Test requires further review
	NotSupported = "Not supported"  // Test exercises features not supported in Cloud 2
	SkippedOSS   = "Skipped OSS"    // Test was skipped in OSS
	FixRequired  = "Fix required: " // Test requires fix per linked issue
	FlakyTest    = "Flaky test"     // Test was skipped in OSS as flakey
)

// Ensure the server can query with default databases (via param) and default retention policy
func TestServer_Query_DefaultDBAndRP(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: fmt.Sprintf(`cpu value=1.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:00Z").UnixNano())},
	}

	test.addQueries([]*Query{
		{
			name:    "default db and rp",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T01:00:00Z",1]]}]}]}`,
		},
		{
			name:    "default rp exists",
			command: `show retention policies ON db0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["name","duration","shardGroupDuration","replicaN","default"],"values":[["autogen","0s","168h0m0s",1,false],["rp0","0s","168h0m0s",1,true]]}]}]}`,
			skip:    "not supported",
		},
		{
			name:    "default rp",
			command: `SELECT * FROM db0..cpu GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T01:00:00Z",1]]}]}]}`,
		},
		{
			name:    "default dp",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM rp0.cpu GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T01:00:00Z",1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can have a database with multiple measurements.
func TestServer_Query_Multiple_Measurements(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	// Make sure we do writes for measurements that will span across shards
	writes := []string{
		fmt.Sprintf("cpu,host=server01 value=100,core=4 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf("cpu1,host=server02 value=50,core=2 %d", mustParseTime(time.RFC3339Nano, "2015-01-01T00:00:00Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "measurement in one shard but not another shouldn't panic server",
			command: `SELECT host,value  FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host","value"],"values":[["2000-01-01T00:00:00Z","server01",100]]}]}]}`,
		},
		{
			name:    "measurement in one shard but not another shouldn't panic server",
			command: `SELECT host,value  FROM db0.rp0.cpu GROUP BY host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","host","value"],"values":[["2000-01-01T00:00:00Z","server01",100]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query with relative time.
func TestServer_Query_SelectRelativeTime(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()
	yesterday := yesterday()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: fmt.Sprintf("cpu,host=server01 value=100 %s\ncpu,host=server01 value=200 %s", strconv.FormatInt(yesterday.UnixNano(), 10), strconv.FormatInt(now.UnixNano(), 10))},
	}

	test.addQueries([]*Query{
		{
			name:    "single point with time pre-calculated for past time queries yesterday",
			command: `SELECT * FROM db0.rp0.cpu where time >= '` + yesterday.Add(-1*time.Minute).Format(time.RFC3339Nano) + `' GROUP BY *`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[["%s",100],["%s",200]]}]}]}`, yesterday.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)),
		},
		{
			name:    "single point with time pre-calculated for relative time queries now",
			command: `SELECT * FROM db0.rp0.cpu where time >= now() - 1m GROUP BY *`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[["%s",200]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_IntMax(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`intmax value=%s %d`, maxInt64(), mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`intmax value=%s %d`, maxInt64(), mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:00Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "large mean and stddev - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEAN(value), STDDEV(value) FROM intmax`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmax","columns":["time","mean","stddev"],"values":[["1970-01-01T00:00:00Z",` + maxInt64() + `,0]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_TimeZone(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	var writes []string
	for _, start := range []time.Time{
		// One day before DST starts.
		time.Date(2000, 4, 1, 0, 0, 0, 0, LosAngeles),
		// Middle of DST. No change.
		time.Date(2000, 6, 1, 0, 0, 0, 0, LosAngeles),
		// One day before DST ends.
		time.Date(2000, 10, 28, 0, 0, 0, 0, LosAngeles),
	} {
		ts := start
		// Write every hour for 4 days.
		for i := 0; i < 24*4; i++ {
			writes = append(writes, fmt.Sprintf(`cpu,interval=daily value=0 %d`, ts.UnixNano()))
			ts = ts.Add(time.Hour)
		}

		// Write every 5 minutes for 3 hours. Start at 1 on the day with DST.
		ts = start.Add(25 * time.Hour)
		for i := 0; i < 12*3; i++ {
			writes = append(writes, fmt.Sprintf(`cpu,interval=hourly value=0 %d`, ts.UnixNano()))
			ts = ts.Add(5 * time.Minute)
		}
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "timezone offset - dst start - daily",
			command: `SELECT count(value) FROM cpu WHERE time >= '2000-04-02T00:00:00-08:00' AND time < '2000-04-04T00:00:00-07:00' AND interval = 'daily' GROUP BY time(1d) TZ('America/Los_Angeles')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-04-02T00:00:00-08:00",23],["2000-04-03T00:00:00-07:00",24]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "timezone offset - no change - daily",
			command: `SELECT count(value) FROM cpu WHERE time >= '2000-06-01T00:00:00-07:00' AND time < '2000-06-03T00:00:00-07:00' AND interval = 'daily' GROUP BY time(1d) TZ('America/Los_Angeles')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-06-01T00:00:00-07:00",24],["2000-06-02T00:00:00-07:00",24]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "timezone offset - dst end - daily",
			command: `SELECT count(value) FROM cpu WHERE time >= '2000-10-29T00:00:00-07:00' AND time < '2000-10-31T00:00:00-08:00' AND interval = 'daily' GROUP BY time(1d) TZ('America/Los_Angeles')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-10-29T00:00:00-07:00",25],["2000-10-30T00:00:00-08:00",24]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "timezone offset - dst start - hourly",
			command: `SELECT count(value) FROM cpu WHERE time >= '2000-04-02T01:00:00-08:00' AND time < '2000-04-02T04:00:00-07:00' AND interval = 'hourly' GROUP BY time(1h) TZ('America/Los_Angeles')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-04-02T01:00:00-08:00",12],["2000-04-02T03:00:00-07:00",12]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "timezone offset - no change - hourly",
			command: `SELECT count(value) FROM cpu WHERE time >= '2000-06-02T01:00:00-07:00' AND time < '2000-06-02T03:00:00-07:00' AND interval = 'hourly' GROUP BY time(1h) TZ('America/Los_Angeles')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-06-02T01:00:00-07:00",12],["2000-06-02T02:00:00-07:00",12]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "timezone offset - dst end - hourly",
			command: `SELECT count(value) FROM cpu WHERE time >= '2000-10-29T01:00:00-07:00' AND time < '2000-10-29T02:00:00-08:00' AND interval = 'hourly' GROUP BY time(1h) TZ('America/Los_Angeles')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-10-29T01:00:00-07:00",12],["2000-10-29T01:00:00-08:00",12]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server correctly supports data with identical tag values.
func TestServer_Query_IdenticalTagValues(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf("cpu,t1=val1 value=1 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf("cpu,t2=val2 value=2 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
		fmt.Sprintf("cpu,t1=val2 value=3 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:02:00Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "measurements with identical tag values - SELECT *, no GROUP BY",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"t1":"","t2":"val2"},"columns":["time","value"],"values":[["2000-01-01T00:01:00Z",2]]},{"name":"cpu","tags":{"t1":"val1","t2":""},"columns":["time","value"],"values":[["2000-01-01T00:00:00Z",1]]},{"name":"cpu","tags":{"t1":"val2","t2":""},"columns":["time","value"],"values":[["2000-01-01T00:02:00Z",3]]}]}]}`,
		},
		{
			name:    "measurements with identical tag values - SELECT *, with GROUP BY",
			command: `SELECT value FROM db0.rp0.cpu GROUP BY t1,t2`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"t1":"","t2":"val2"},"columns":["time","value"],"values":[["2000-01-01T00:01:00Z",2]]},{"name":"cpu","tags":{"t1":"val1","t2":""},"columns":["time","value"],"values":[["2000-01-01T00:00:00Z",1]]},{"name":"cpu","tags":{"t1":"val2","t2":""},"columns":["time","value"],"values":[["2000-01-01T00:02:00Z",3]]}]}]}`,
		},
		{
			name:    "measurements with identical tag values - SELECT value no GROUP BY",
			command: `SELECT value FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",1],["2000-01-01T00:01:00Z",2],["2000-01-01T00:02:00Z",3]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle a query that involves accessing no shards.
func TestServer_Query_NoShards(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu,host=server01 value=1 ` + strconv.FormatInt(now.UnixNano(), 10)},
	}

	test.addQueries([]*Query{
		{
			name:    "selecting value should succeed",
			command: `SELECT value FROM db0.rp0.cpu WHERE time < now() - 1d`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query a non-existent field
func TestServer_Query_NonExistent(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu,host=server01 value=1 ` + strconv.FormatInt(now.UnixNano(), 10)},
	}

	test.addQueries([]*Query{
		{
			name:    "selecting value should succeed",
			command: `SELECT value FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",1]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "selecting non-existent should succeed",
			command: `SELECT foo FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can perform basic math
func TestServer_Query_Math(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()
	writes := []string{
		"float value=42 " + strconv.FormatInt(now.UnixNano(), 10),
		"integer value=42i " + strconv.FormatInt(now.UnixNano(), 10),
	}

	test := NewTest("db", "rp")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "SELECT multiple of float value",
			command: `SELECT value * 2 from db.rp.float`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"float","columns":["time","value"],"values":[["%s",84]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT multiple of float value",
			command: `SELECT 2 * value from db.rp.float`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"float","columns":["time","value"],"values":[["%s",84]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT multiple of integer value",
			command: `SELECT value * 2 from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","value"],"values":[["%s",84]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT float multiple of integer value",
			command: `SELECT value * 2.0 from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","value"],"values":[["%s",84]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT square of float value",
			command: `SELECT value * value from db.rp.float`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"float","columns":["time","value_value"],"values":[["%s",1764]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT square of integer value",
			command: `SELECT value * value from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","value_value"],"values":[["%s",1764]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT square of integer, float value",
			command: `SELECT value * value,float from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","value_value","float"],"values":[["%s",1764,null]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT square of integer value with alias",
			command: `SELECT value * value as square from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","square"],"values":[["%s",1764]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT sum of aggregates",
			command: `SELECT max(value) + min(value) from db.rp.integer`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","max_min"],"values":[["1970-01-01T00:00:00Z",84]]}]}]}`,
		},
		{
			name:    "SELECT square of enclosed integer value",
			command: `SELECT ((value) * (value)) from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","value_value"],"values":[["%s",1764]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "SELECT square of enclosed integer value",
			command: `SELECT (value * value) from db.rp.integer`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"integer","columns":["time","value_value"],"values":[["%s",1764]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various simple non_negative_derivative queries.
func TestServer_Query_SelectRawNonNegativeDerivative(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010021000000000
cpu value=15 1278010022000000000
cpu value=10 1278010023000000000
cpu value=20 1278010024000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate single non_negative_derivative",
			command: `SELECT non_negative_derivative(value) from db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","non_negative_derivative"],"values":[["2010-07-01T18:47:02Z",5],["2010-07-01T18:47:04Z",10]]}]}]}`,
		},
		{
			name:    "calculate single non_negative_derivative",
			command: `SELECT non_negative_derivative(value, 10s) from db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","non_negative_derivative"],"values":[["2010-07-01T18:47:02Z",50],["2010-07-01T18:47:04Z",100]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time derivative queries.
func TestServer_Query_SelectGroupByTimeDerivative(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=15 1278010021000000000
cpu value=20 1278010022000000000
cpu value=25 1278010023000000000

cpu0,host=server01 ticks=10,total=100 1278010020000000000
cpu0,host=server01 ticks=30,total=100 1278010021000000000
cpu0,host=server01 ticks=32,total=100 1278010022000000000
cpu0,host=server01 ticks=47,total=100 1278010023000000000
cpu0,host=server02 ticks=40,total=100 1278010020000000000
cpu0,host=server02 ticks=45,total=100 1278010021000000000
cpu0,host=server02 ticks=84,total=100 1278010022000000000
cpu0,host=server02 ticks=101,total=100 1278010023000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate derivative of count with unit default (2s) group by time",
			command: `SELECT derivative(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of count with unit 4s group by time",
			command: `SELECT derivative(count(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",4],["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of mean with unit default (2s) group by time",
			command: `SELECT derivative(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of mean with unit 4s group by time",
			command: `SELECT derivative(mean(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of median with unit default (2s) group by time",
			command: `SELECT derivative(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of median with unit 4s group by time",
			command: `SELECT derivative(median(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of mode with unit default (2s) group by time",
			command: `SELECT derivative(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of mode with unit 4s group by time",
			command: `SELECT derivative(mode(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},

		{
			name:    "calculate derivative of sum with unit default (2s) group by time",
			command: `SELECT derivative(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of sum with unit 4s group by time",
			command: `SELECT derivative(sum(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",40]]}]}]}`,
		},
		{
			name:    "calculate derivative of first with unit default (2s) group by time",
			command: `SELECT derivative(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of first with unit 4s group by time",
			command: `SELECT derivative(first(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of last with unit default (2s) group by time",
			command: `SELECT derivative(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of last with unit 4s group by time",
			command: `SELECT derivative(last(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of min with unit default (2s) group by time",
			command: `SELECT derivative(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of min with unit 4s group by time",
			command: `SELECT derivative(min(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of max with unit default (2s) group by time",
			command: `SELECT derivative(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of max with unit 4s group by time",
			command: `SELECT derivative(max(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of percentile with unit default (2s) group by time",
			command: `SELECT derivative(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate derivative of percentile with unit 4s group by time",
			command: `SELECT derivative(percentile(value, 50), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate derivative of ticks divided by aggregate",
			command: `SELECT non_negative_derivative(mean(ticks), 1s) / last(total) * 100 AS usage FROM db0.rp0.cpu0 WHERE time >= '2010-07-01 18:47:00' AND time <= '2010-07-01 18:47:03' GROUP BY host, time(1s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu0","tags":{"host":"server01"},"columns":["time","usage"],"values":[["2010-07-01T18:47:00Z",null],["2010-07-01T18:47:01Z",20],["2010-07-01T18:47:02Z",2],["2010-07-01T18:47:03Z",15]]},{"name":"cpu0","tags":{"host":"server02"},"columns":["time","usage"],"values":[["2010-07-01T18:47:00Z",null],["2010-07-01T18:47:01Z",5],["2010-07-01T18:47:02Z",39],["2010-07-01T18:47:03Z",17]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query with the count aggregate function
func TestServer_Query_Count(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	writes := []string{
		`cpu,host=server01 value=1.0 ` + strconv.FormatInt(now.UnixNano(), 10),
		`ram value1=1.0,value2=2.0 ` + strconv.FormatInt(now.UnixNano(), 10),
	}

	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	hourAgo := now.Add(-time.Hour).UTC()

	test.addQueries([]*Query{
		{
			name:    "selecting count(value) should succeed",
			command: `SELECT count(value) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "selecting count(value) with where time should return result",
			command: fmt.Sprintf(`SELECT count(value) FROM db0.rp0.cpu WHERE time >= '%s'`, hourAgo.Format(time.RFC3339Nano)),
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["%s",1]]}]}]}`, hourAgo.Format(time.RFC3339Nano)),
		},
		{
			name:    "selecting count(value) with filter that excludes all results should return 0",
			command: fmt.Sprintf(`SELECT count(value) FROM db0.rp0.cpu WHERE value=100 AND time >= '%s'`, hourAgo.Format(time.RFC3339Nano)),
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "selecting count(value1) with matching filter against value2 should return correct result",
			command: fmt.Sprintf(`SELECT count(value1) FROM db0.rp0.ram WHERE value2=2 AND time >= '%s'`, hourAgo.Format(time.RFC3339Nano)),
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"ram","columns":["time","count"],"values":[["%s",1]]}]}]}`, hourAgo.Format(time.RFC3339Nano)),
		},
		{
			name:    "selecting count(value1) with non-matching filter against value2 should return correct result",
			command: fmt.Sprintf(`SELECT count(value1) FROM db0.rp0.ram WHERE value2=3 AND time >= '%s'`, hourAgo.Format(time.RFC3339Nano)),
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "selecting count(*) should expand the wildcard",
			command: `SELECT count(*) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count_value"],"values":[["1970-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "selecting count(2) should error",
			command: `SELECT count(2) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"expected field argument in count()"}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can limit concurrent series.
func TestServer_Query_MaxSelectSeriesN(t *testing.T) {
	s := OpenServer(t, func(o *launcher.InfluxdOpts) {
		o.CoordinatorConfig.MaxSelectSeriesN = 3
	})
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu,host=server01 value=1.0 0`},
		&Write{data: `cpu,host=server02 value=1.0 0`},
		&Write{data: `cpu,host=server03 value=1.0 0`},
		&Write{data: `cpu,host=server04 value=1.0 0`},
	}

	test.addQueries([]*Query{
		{
			name:    "exceeed max series",
			command: `SELECT COUNT(value) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"max-select-series limit exceeded: (4/3)"}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query with Now().
func TestServer_Query_Now(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu,host=server01 value=1.0 ` + strconv.FormatInt(now.UnixNano(), 10)},
	}

	test.addQueries([]*Query{
		{
			name:    "where with time < now() should work",
			command: `SELECT * FROM db0.rp0.cpu where time < now()`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host","value"],"values":[["%s","server01",1]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "where with time < now() and GROUP BY * should work",
			command: `SELECT * FROM db0.rp0.cpu where time < now() GROUP BY *`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[["%s",1]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "where with time > now() should return an empty result",
			command: `SELECT * FROM db0.rp0.cpu where time > now()`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "where with time > now() with GROUP BY * should return an empty result",
			command: `SELECT * FROM db0.rp0.cpu where time > now() GROUP BY *`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query with epoch precisions.
func TestServer_Query_EpochPrecision(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu,host=server01 value=1.0 ` + strconv.FormatInt(now.UnixNano(), 10)},
	}

	test.addQueries([]*Query{
		{
			name:    "nanosecond precision",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			params:  url.Values{"epoch": []string{"n"}},
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[[%d,1]]}]}]}`, now.UnixNano()),
		},
		{
			name:    "microsecond precision",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			params:  url.Values{"epoch": []string{"u"}},
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[[%d,1]]}]}]}`, now.UnixNano()/int64(time.Microsecond)),
		},
		{
			name:    "millisecond precision",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			params:  url.Values{"epoch": []string{"ms"}},
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[[%d,1]]}]}]}`, now.UnixNano()/int64(time.Millisecond)),
		},
		{
			name:    "second precision",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			params:  url.Values{"epoch": []string{"s"}},
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[[%d,1]]}]}]}`, now.UnixNano()/int64(time.Second)),
		},
		{
			name:    "minute precision",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			params:  url.Values{"epoch": []string{"m"}},
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[[%d,1]]}]}]}`, now.UnixNano()/int64(time.Minute)),
		},
		{
			name:    "hour precision",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			params:  url.Values{"epoch": []string{"h"}},
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[[%d,1]]}]}]}`, now.UnixNano()/int64(time.Hour)),
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server works with tag queries.
func TestServer_Query_Tags(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	writes := []string{
		fmt.Sprintf("cpu,host=server01 value=100,core=4 %d", now.UnixNano()),
		fmt.Sprintf("cpu,host=server02 value=50,core=2 %d", now.Add(1).UnixNano()),

		fmt.Sprintf("cpu1,host=server01,region=us-west value=100 %d", mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
		fmt.Sprintf("cpu1,host=server02 value=200 %d", mustParseTime(time.RFC3339Nano, "2010-02-28T01:03:37.703820946Z").UnixNano()),
		fmt.Sprintf("cpu1,host=server03 value=300 %d", mustParseTime(time.RFC3339Nano, "2012-02-28T01:03:38.703820946Z").UnixNano()),

		fmt.Sprintf("cpu2,host=server01 value=100 %d", mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
		fmt.Sprintf("cpu2 value=200 %d", mustParseTime(time.RFC3339Nano, "2012-02-28T01:03:38.703820946Z").UnixNano()),

		fmt.Sprintf("cpu3,company=acme01 value=100 %d", mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
		fmt.Sprintf("cpu3 value=200 %d", mustParseTime(time.RFC3339Nano, "2012-02-28T01:03:38.703820946Z").UnixNano()),

		fmt.Sprintf("status_code,url=http://www.example.com value=404 %d", mustParseTime(time.RFC3339Nano, "2015-07-22T08:13:54.929026672Z").UnixNano()),
		fmt.Sprintf("status_code,url=https://influxdb.com value=418 %d", mustParseTime(time.RFC3339Nano, "2015-07-22T09:52:24.914395083Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "tag without field should return error",
			command: `SELECT host FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"statement must have at least one field in select clause"}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): tags should stream as values
		},
		{
			name:    "field with tag should succeed",
			command: `SELECT host, value FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host","value"],"values":[["%s","server01",100],["%s","server02",50]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "field with tag and GROUP BY should succeed",
			command: `SELECT host, value FROM db0.rp0.cpu GROUP BY host`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","host","value"],"values":[["%s","server01",100]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","host","value"],"values":[["%s","server02",50]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "field with two tags should succeed",
			command: `SELECT host, value, core FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host","value","core"],"values":[["%s","server01",100,4],["%s","server02",50,2]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "field with two tags and GROUP BY should succeed",
			command: `SELECT host, value, core FROM db0.rp0.cpu GROUP BY host`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","host","value","core"],"values":[["%s","server01",100,4]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","host","value","core"],"values":[["%s","server02",50,2]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "select * with tags should succeed",
			command: `SELECT * FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core","host","value"],"values":[["%s",4,"server01",100],["%s",2,"server02",50]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "select * with tags with GROUP BY * should succeed",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","core","value"],"values":[["%s",4,100]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","core","value"],"values":[["%s",2,50]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "group by tag",
			command: `SELECT value FROM db0.rp0.cpu GROUP by host`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[["%s",100]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","value"],"values":[["%s",50]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		{
			name:    "single field (EQ tag value1)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server01'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (2 EQ tags)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server01' AND region = 'us-west'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (OR different tags)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server03' OR region = 'us-west'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2012-02-28T01:03:38.703820946Z",300],["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (OR with non-existent tag value)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server01' OR host = 'server66'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (OR with all tag values)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server01' OR host = 'server02' OR host = 'server03'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2010-02-28T01:03:37.703820946Z",200],["2012-02-28T01:03:38.703820946Z",300],["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (1 EQ and 1 NEQ tag)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server01' AND region != 'us-west'`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "single field (EQ tag value2)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host = 'server02'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2010-02-28T01:03:37.703820946Z",200]]}]}]}`,
		},
		{
			name:    "single field (NEQ tag value1)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host != 'server01'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2010-02-28T01:03:37.703820946Z",200],["2012-02-28T01:03:38.703820946Z",300]]}]}]}`,
		},
		{
			name:    "single field (NEQ tag value1 AND NEQ tag value2)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host != 'server01' AND host != 'server02'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2012-02-28T01:03:38.703820946Z",300]]}]}]}`,
		},
		{
			name:    "single field (NEQ tag value1 OR NEQ tag value2)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host != 'server01' OR host != 'server02'`, // Yes, this is always true, but that's the point.
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2010-02-28T01:03:37.703820946Z",200],["2012-02-28T01:03:38.703820946Z",300],["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (NEQ tag value1 AND NEQ tag value2 AND NEQ tag value3)",
			command: `SELECT value FROM db0.rp0.cpu1 WHERE host != 'server01' AND host != 'server02' AND host != 'server03'`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "single field (NEQ tag value1, point without any tags)",
			command: `SELECT value FROM db0.rp0.cpu2 WHERE host != 'server01'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu2","columns":["time","value"],"values":[["2012-02-28T01:03:38.703820946Z",200]]}]}]}`,
		},
		{
			name:    "single field (NEQ tag value1, point without any tags)",
			command: `SELECT value FROM db0.rp0.cpu3 WHERE company !~ /acme01/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu3","columns":["time","value"],"values":[["2012-02-28T01:03:38.703820946Z",200]]}]}]}`,
		},
		{
			name:    "single field (regex tag match)",
			command: `SELECT value FROM db0.rp0.cpu3 WHERE company =~ /acme01/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu3","columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (regex tag match)",
			command: `SELECT value FROM db0.rp0.cpu3 WHERE company !~ /acme[23]/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu3","columns":["time","value"],"values":[["2012-02-28T01:03:38.703820946Z",200],["2015-02-28T01:03:36.703820946Z",100]]}]}]}`,
		},
		{
			name:    "single field (regex tag match with escaping)",
			command: `SELECT value FROM db0.rp0.status_code WHERE url !~ /https\:\/\/influxdb\.com/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"status_code","columns":["time","value"],"values":[["2015-07-22T08:13:54.929026672Z",404]]}]}]}`,
		},
		{
			name:    "single field (regex tag match with escaping)",
			command: `SELECT value FROM db0.rp0.status_code WHERE url =~ /https\:\/\/influxdb\.com/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"status_code","columns":["time","value"],"values":[["2015-07-22T09:52:24.914395083Z",418]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time moving average queries.
func TestServer_Query_SelectGroupByTimeMovingAverage(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=15 1278010021000000000
cpu value=20 1278010022000000000
cpu value=25 1278010023000000000
cpu value=30 1278010024000000000
cpu value=35 1278010025000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate moving average of count",
			command: `SELECT moving_average(count(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",1],["2010-07-01T18:47:02Z",2],["2010-07-01T18:47:04Z",2]]}]}]}`,
		},
		{
			name:    "calculate moving average of mean",
			command: `SELECT moving_average(mean(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",17.5],["2010-07-01T18:47:04Z",27.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of median",
			command: `SELECT moving_average(median(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",17.5],["2010-07-01T18:47:04Z",27.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of mode",
			command: `SELECT moving_average(mode(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",15],["2010-07-01T18:47:04Z",25]]}]}]}`,
		},
		{
			name:    "calculate moving average of sum",
			command: `SELECT moving_average(sum(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",35],["2010-07-01T18:47:04Z",55]]}]}]}`,
		},
		{
			name:    "calculate moving average of first",
			command: `SELECT moving_average(first(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",15],["2010-07-01T18:47:04Z",25]]}]}]}`,
		},
		{
			name:    "calculate moving average of last",
			command: `SELECT moving_average(last(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",20],["2010-07-01T18:47:04Z",30]]}]}]}`,
		},
		{
			name:    "calculate moving average of min",
			command: `SELECT moving_average(min(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",15],["2010-07-01T18:47:04Z",25]]}]}]}`,
		},
		{
			name:    "calculate moving average of max",
			command: `SELECT moving_average(max(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",20],["2010-07-01T18:47:04Z",30]]}]}]}`,
		},
		{
			name:    "calculate moving average of percentile",
			command: `SELECT moving_average(percentile(value, 50), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",15],["2010-07-01T18:47:04Z",25]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time moving average queries.
func TestServer_Query_SelectGroupByTimeMovingAverageWithFill(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=15 1278010021000000000
cpu value=30 1278010024000000000
cpu value=35 1278010025000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate moving average of count with fill 0",
			command: `SELECT moving_average(count(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",1],["2010-07-01T18:47:02Z",1],["2010-07-01T18:47:04Z",1]]}]}]}`,
		},
		{
			name:    "calculate moving average of count with fill previous",
			command: `SELECT moving_average(count(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",2],["2010-07-01T18:47:04Z",2]]}]}]}`,
		},
		{
			name:    "calculate moving average of mean with fill 0",
			command: `SELECT moving_average(mean(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",6.25],["2010-07-01T18:47:02Z",6.25],["2010-07-01T18:47:04Z",16.25]]}]}]}`,
		},
		{
			name:    "calculate moving average of mean with fill previous",
			command: `SELECT moving_average(mean(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",12.5],["2010-07-01T18:47:04Z",22.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of median with fill 0",
			command: `SELECT moving_average(median(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",6.25],["2010-07-01T18:47:02Z",6.25],["2010-07-01T18:47:04Z",16.25]]}]}]}`,
		},
		{
			name:    "calculate moving average of median with fill previous",
			command: `SELECT moving_average(median(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",12.5],["2010-07-01T18:47:04Z",22.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of mode with fill 0",
			command: `SELECT moving_average(mode(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",5],["2010-07-01T18:47:02Z",5],["2010-07-01T18:47:04Z",15]]}]}]}`,
		},
		{
			name:    "calculate moving average of mode with fill previous",
			command: `SELECT moving_average(mode(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",10],["2010-07-01T18:47:04Z",20]]}]}]}`,
		},
		{
			name:    "calculate moving average of sum with fill 0",
			command: `SELECT moving_average(sum(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",12.5],["2010-07-01T18:47:02Z",12.5],["2010-07-01T18:47:04Z",32.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of sum with fill previous",
			command: `SELECT moving_average(sum(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",25],["2010-07-01T18:47:04Z",45]]}]}]}`,
		},
		{
			name:    "calculate moving average of first with fill 0",
			command: `SELECT moving_average(first(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",5],["2010-07-01T18:47:02Z",5],["2010-07-01T18:47:04Z",15]]}]}]}`,
		},
		{
			name:    "calculate moving average of first with fill previous",
			command: `SELECT moving_average(first(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",10],["2010-07-01T18:47:04Z",20]]}]}]}`,
		},
		{
			name:    "calculate moving average of last with fill 0",
			command: `SELECT moving_average(last(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",7.5],["2010-07-01T18:47:02Z",7.5],["2010-07-01T18:47:04Z",17.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of last with fill previous",
			command: `SELECT moving_average(last(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",15],["2010-07-01T18:47:04Z",25]]}]}]}`,
		},
		{
			name:    "calculate moving average of min with fill 0",
			command: `SELECT moving_average(min(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",5],["2010-07-01T18:47:02Z",5],["2010-07-01T18:47:04Z",15]]}]}]}`,
		},
		{
			name:    "calculate moving average of min with fill previous",
			command: `SELECT moving_average(min(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",10],["2010-07-01T18:47:04Z",20]]}]}]}`,
		},
		{
			name:    "calculate moving average of max with fill 0",
			command: `SELECT moving_average(max(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",7.5],["2010-07-01T18:47:02Z",7.5],["2010-07-01T18:47:04Z",17.5]]}]}]}`,
		},
		{
			name:    "calculate moving average of max with fill previous",
			command: `SELECT moving_average(max(value), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",15],["2010-07-01T18:47:04Z",25]]}]}]}`,
		},
		{
			name:    "calculate moving average of percentile with fill 0",
			command: `SELECT moving_average(percentile(value, 50), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:00Z",5],["2010-07-01T18:47:02Z",5],["2010-07-01T18:47:04Z",15]]}]}]}`,
		},
		{
			name:    "calculate moving average of percentile with fill previous",
			command: `SELECT moving_average(percentile(value, 50), 2) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:05' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","moving_average"],"values":[["2010-07-01T18:47:02Z",10],["2010-07-01T18:47:04Z",20]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
} // Ensure the server correctly queries with an alias.
func TestServer_Query_Alias(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf("cpu value=1i,steps=3i %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf("cpu value=2i,steps=4i %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "baseline query - SELECT * FROM db0.rp0.cpu",
			command: `SELECT * FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","steps","value"],"values":[["2000-01-01T00:00:00Z",3,1],["2000-01-01T00:01:00Z",4,2]]}]}]}`,
		},
		{
			name:    "basic query with alias - SELECT steps, value as v FROM db0.rp0.cpu",
			command: `SELECT steps, value as v FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","steps","v"],"values":[["2000-01-01T00:00:00Z",3,1],["2000-01-01T00:01:00Z",4,2]]}]}]}`,
		},
		{
			name:    "double aggregate sum - SELECT sum(value), sum(steps) FROM db0.rp0.cpu",
			command: `SELECT sum(value), sum(steps) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum","sum_1"],"values":[["1970-01-01T00:00:00Z",3,7]]}]}]}`,
		},
		{
			name:    "double aggregate sum reverse order - SELECT sum(steps), sum(value) FROM db0.rp0.cpu",
			command: `SELECT sum(steps), sum(value) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum","sum_1"],"values":[["1970-01-01T00:00:00Z",7,3]]}]}]}`,
		},
		{
			name:    "double aggregate sum with alias - SELECT sum(value) as sumv, sum(steps) as sums FROM db0.rp0.cpu",
			command: `SELECT sum(value) as sumv, sum(steps) as sums FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sumv","sums"],"values":[["1970-01-01T00:00:00Z",3,7]]}]}]}`,
		},
		{
			name:    "double aggregate with same value - SELECT sum(value), mean(value) FROM db0.rp0.cpu",
			command: `SELECT sum(value), mean(value) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum","mean"],"values":[["1970-01-01T00:00:00Z",3,1.5]]}]}]}`,
		},
		{
			name:    "double aggregate with same value and same alias - SELECT mean(value) as mv, max(value) as mv FROM db0.rp0.cpu",
			command: `SELECT mean(value) as mv, max(value) as mv FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mv","mv"],"values":[["1970-01-01T00:00:00Z",1.5,2]]}]}]}`,
		},
		{
			name:    "double aggregate with non-existent field - SELECT mean(value), max(foo) FROM db0.rp0.cpu",
			command: `SELECT mean(value), max(foo) FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean","max"],"values":[["1970-01-01T00:00:00Z",1.5,null]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time cumulative sum queries.
func TestServer_Query_SelectGroupByTimeCumulativeSum(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=15 1278010021000000000
cpu value=20 1278010022000000000
cpu value=25 1278010023000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate cumulative sum of count",
			command: `SELECT cumulative_sum(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",4]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of mean",
			command: `SELECT cumulative_sum(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",12.5],["2010-07-01T18:47:02Z",35]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of median",
			command: `SELECT cumulative_sum(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",12.5],["2010-07-01T18:47:02Z",35]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of mode",
			command: `SELECT cumulative_sum(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of sum",
			command: `SELECT cumulative_sum(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",25],["2010-07-01T18:47:02Z",70]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of first",
			command: `SELECT cumulative_sum(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of last",
			command: `SELECT cumulative_sum(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",40]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of min",
			command: `SELECT cumulative_sum(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of max",
			command: `SELECT cumulative_sum(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",40]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of percentile",
			command: `SELECT cumulative_sum(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time cumulative sum queries with fill.
func TestServer_Query_SelectGroupByTimeCumulativeSumWithFill(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=20 1278010021000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate cumulative sum of count with fill 0",
			command: `SELECT cumulative_sum(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",2]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of count with fill previous",
			command: `SELECT cumulative_sum(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",4]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of mean with fill 0",
			command: `SELECT cumulative_sum(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",15]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of mean with fill previous",
			command: `SELECT cumulative_sum(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of median with fill 0",
			command: `SELECT cumulative_sum(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",15]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of median with fill previous",
			command: `SELECT cumulative_sum(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of mode with fill 0",
			command: `SELECT cumulative_sum(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of mode with fill previous",
			command: `SELECT cumulative_sum(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of sum with fill 0",
			command: `SELECT cumulative_sum(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",30],["2010-07-01T18:47:02Z",30]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of sum with fill previous",
			command: `SELECT cumulative_sum(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",30],["2010-07-01T18:47:02Z",60]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of first with fill 0",
			command: `SELECT cumulative_sum(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of first with fill previous",
			command: `SELECT cumulative_sum(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of last with fill 0",
			command: `SELECT cumulative_sum(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of last with fill previous",
			command: `SELECT cumulative_sum(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",40]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of min with fill 0",
			command: `SELECT cumulative_sum(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of min with fill previous",
			command: `SELECT cumulative_sum(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of max with fill 0",
			command: `SELECT cumulative_sum(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of max with fill previous",
			command: `SELECT cumulative_sum(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",40]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of percentile with fill 0",
			command: `SELECT cumulative_sum(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate cumulative sum of percentile with fill previous",
			command: `SELECT cumulative_sum(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_Int(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`int value=45 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		// int64
		{
			name:    "stddev with just one point - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT STDDEV(value) FROM int`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"int","columns":["time","stddev"],"values":[["1970-01-01T00:00:00Z",null]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_MathWithFill(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=15 1278010020000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "multiplication with fill previous",
			command: `SELECT 4*mean(value) FROM db0.rp0.cpu WHERE time >= '2010-07-01 18:47:00' AND time < '2010-07-01 18:48:30' GROUP BY time(30s) FILL(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2010-07-01T18:47:00Z",60],["2010-07-01T18:47:30Z",60],["2010-07-01T18:48:00Z",60]]}]}]}`,
		},
		{
			name:    "multiplication of mode value with fill previous",
			command: `SELECT 4*mode(value) FROM db0.rp0.cpu WHERE time >= '2010-07-01 18:47:00' AND time < '2010-07-01 18:48:30' GROUP BY time(30s) FILL(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mode"],"values":[["2010-07-01T18:47:00Z",60],["2010-07-01T18:47:30Z",60],["2010-07-01T18:48:00Z",60]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// mergeMany ensures that when merging many series together and some of them have a different number
// of points than others in a group by interval the results are correct
func TestServer_Query_MergeMany(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")

	var writes []string
	for i := 1; i < 11; i++ {
		for j := 1; j < 5+i%3; j++ {
			data := fmt.Sprintf(`cpu,host=server_%d value=22 %d`, i, time.Unix(int64(j), int64(0)).UTC().UnixNano())
			writes = append(writes, data)
		}
	}
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "GROUP by time",
			command: `SELECT count(value) FROM db0.rp0.cpu WHERE time >= '1970-01-01T00:00:01Z' AND time <= '1970-01-01T00:00:06Z' GROUP BY time(1s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["1970-01-01T00:00:01Z",10],["1970-01-01T00:00:02Z",10],["1970-01-01T00:00:03Z",10],["1970-01-01T00:00:04Z",10],["1970-01-01T00:00:05Z",7],["1970-01-01T00:00:06Z",3]]}]}]}`,
		},
		{
			skip:    "Skipped OSS",
			name:    "GROUP by tag - FIXME issue #2875",
			command: `SELECT count(value) FROM db0.rp0.cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:00Z' group by host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","count"],"values":[["2000-01-01T00:00:00Z",1]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","count"],"values":[["2000-01-01T00:00:00Z",1]]},{"name":"cpu","tags":{"host":"server03"},"columns":["time","count"],"values":[["2000-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "GROUP by field",
			command: `SELECT count(value) FROM db0.rp0.cpu group by value`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"value":""},"columns":["time","count"],"values":[["1970-01-01T00:00:00Z",50]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_SelectGroupByTime_MultipleAggregates(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `test,t=a x=1i 1000000000
test,t=b y=1i 1000000000
test,t=a x=2i 2000000000
test,t=b y=2i 2000000000
test,t=a x=3i 3000000000
test,t=b y=3i 3000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "two aggregates with a group by host",
			command: `SELECT mean(x) as x, mean(y) as y from db0.rp0.test where time >= 1s and time < 4s group by t, time(1s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"test","tags":{"t":"a"},"columns":["time","x","y"],"values":[["1970-01-01T00:00:01Z",1,null],["1970-01-01T00:00:02Z",2,null],["1970-01-01T00:00:03Z",3,null]]},{"name":"test","tags":{"t":"b"},"columns":["time","x","y"],"values":[["1970-01-01T00:00:01Z",null,1],["1970-01-01T00:00:02Z",null,2],["1970-01-01T00:00:03Z",null,3]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Regex(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu1,host=server01 value=10 %d`, mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
		fmt.Sprintf(`cpu2,host=server01 value=20 %d`, mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
		fmt.Sprintf(`cpu3,host=server01 value=30 %d`, mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "default db and rp",
			command: `SELECT * FROM /cpu[13]/`,
			params:  url.Values{"db": []string{"db0"}},
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","host","value"],"values":[["2015-02-28T01:03:36.703820946Z","server01",10]]},{"name":"cpu3","columns":["time","host","value"],"values":[["2015-02-28T01:03:36.703820946Z","server01",30]]}]}]}`,
		},
		{
			name:    "default db and rp with GROUP BY *",
			command: `SELECT * FROM /cpu[13]/ GROUP BY *`,
			params:  url.Values{"db": []string{"db0"}},
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",10]]},{"name":"cpu3","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",30]]}]}]}`,
		},
		{
			name:    "specifying db and rp",
			command: `SELECT * FROM db0.rp0./cpu[13]/ GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",10]]},{"name":"cpu3","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",30]]}]}]}`,
		},
		{
			name:    "default db and specified rp",
			command: `SELECT * FROM rp0./cpu[13]/ GROUP BY *`,
			params:  url.Values{"db": []string{"db0"}},
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",10]]},{"name":"cpu3","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",30]]}]}]}`,
		},
		{
			name:    "specified db and default rp",
			command: `SELECT * FROM db0../cpu[13]/ GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",10]]},{"name":"cpu3","tags":{"host":"server01"},"columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",30]]}]}]}`,
		},
		{
			name:    "map field type with a regex source",
			command: `SELECT value FROM /cpu[13]/`,
			params:  url.Values{"db": []string{"db0"}},
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu1","columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",10]]},{"name":"cpu3","columns":["time","value"],"values":[["2015-02-28T01:03:36.703820946Z",30]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_Load(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`load,region=us-east,host=serverA value=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`load,region=us-east,host=serverB value=30.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`load,region=us-west,host=serverC value=100.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "group by multiple dimensions",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) FROM load GROUP BY region, host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"load","tags":{"host":"serverA","region":"us-east"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",20]]},{"name":"load","tags":{"host":"serverB","region":"us-east"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",30]]},{"name":"load","tags":{"host":"serverC","region":"us-west"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",100]]}]}]}`,
		},
		{
			name:    "group by multiple dimensions",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value)*2 FROM load`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"load","columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",300]]}]}]}`,
		},
		{
			name:    "group by multiple dimensions",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value)/2 FROM load`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"load","columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",75]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_SLimitAndSOffset(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")

	var writes []string
	for i := 1; i < 10; i++ {
		data := fmt.Sprintf(`cpu,region=us-east,host=server-%d value=%d %d`, i, i, time.Unix(int64(i), int64(0)).UnixNano())
		writes = append(writes, data)
	}
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "SLIMIT 2 SOFFSET 1",
			command: `SELECT count(value) FROM db0.rp0.cpu GROUP BY * SLIMIT 2 SOFFSET 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server-2","region":"us-east"},"columns":["time","count"],"values":[["1970-01-01T00:00:00Z",1]]},{"name":"cpu","tags":{"host":"server-3","region":"us-east"},"columns":["time","count"],"values":[["1970-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "SLIMIT 2 SOFFSET 3",
			command: `SELECT count(value) FROM db0.rp0.cpu GROUP BY * SLIMIT 2 SOFFSET 3`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server-4","region":"us-east"},"columns":["time","count"],"values":[["1970-01-01T00:00:00Z",1]]},{"name":"cpu","tags":{"host":"server-5","region":"us-east"},"columns":["time","count"],"values":[["1970-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "SLIMIT 3 SOFFSET 8",
			command: `SELECT count(value) FROM db0.rp0.cpu GROUP BY * SLIMIT 3 SOFFSET 8`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server-9","region":"us-east"},"columns":["time","count"],"values":[["1970-01-01T00:00:00Z",1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_CumulativeCount(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `events signup=t 1005832000
events signup=t 1048283000
events signup=t 1784832000
events signup=t 2000000000
events signup=t 3084890000
events signup=t 3838400000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "cumulative count",
			command: `SELECT cumulative_sum(count(signup)) from db0.rp0.events where time >= 1s and time < 4s group by time(1s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"events","columns":["time","cumulative_sum"],"values":[["1970-01-01T00:00:01Z",3],["1970-01-01T00:00:02Z",4],["1970-01-01T00:00:03Z",6]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_String(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`stringdata value="first" %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
			fmt.Sprintf(`stringdata value="last" %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:04Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		// strings
		{
			name:    "STDDEV on string data - string",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT STDDEV(value) FROM stringdata`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"stringdata","columns":["time","stddev"],"values":[["1970-01-01T00:00:00Z",null]]}]}]}`,
			skip:    "Skipped OSS", // FIXME(benbjohnson): allow non-float var ref expr in cursor iterator
		},
		{
			name:    "MEAN on string data - string",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEAN(value) FROM stringdata`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"stringdata","columns":["time","mean"],"values":[["1970-01-01T00:00:00Z",0]]}]}]}`,
			skip:    "Skipped OSS", // FIXME(benbjohnson): allow non-float var ref expr in cursor iterator
		},
		{
			name:    "MEDIAN on string data - string",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEDIAN(value) FROM stringdata`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"stringdata","columns":["time","median"],"values":[["1970-01-01T00:00:00Z",null]]}]}]}`,
			skip:    "Skipped OSS", // FIXME(benbjohnson): allow non-float var ref expr in cursor iterator
		},
		{
			name:    "COUNT on string data - string",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(value) FROM stringdata`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"stringdata","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",2]]}]}]}`,
			skip:    "Skipped OSS", // FIXME(benbjohnson): allow non-float var ref expr in cursor iterator
		},
		{
			name:    "FIRST on string data - string",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT FIRST(value) FROM stringdata`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"stringdata","columns":["time","first"],"values":[["2000-01-01T00:00:03Z","first"]]}]}]}`,
			skip:    "Skipped OSS", // FIXME(benbjohnson): allow non-float var ref expr in cursor iterator
		},
		{
			name:    "LAST on string data - string",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT LAST(value) FROM stringdata`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"stringdata","columns":["time","last"],"values":[["2000-01-01T00:00:04Z","last"]]}]}]}`,
			skip:    "Skipped OSS", // FIXME(benbjohnson): allow non-float var ref expr in cursor iterator
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_AggregateSelectors(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`network,host=server01,region=west,core=1 rx=10i,tx=20i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`network,host=server02,region=west,core=2 rx=40i,tx=50i,core=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`network,host=server03,region=east,core=3 rx=40i,tx=55i,core=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		fmt.Sprintf(`network,host=server04,region=east,core=4 rx=40i,tx=60i,core=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
		fmt.Sprintf(`network,host=server05,region=west,core=1 rx=50i,tx=70i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
		fmt.Sprintf(`network,host=server06,region=east,core=2 rx=50i,tx=40i,core=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
		fmt.Sprintf(`network,host=server07,region=west,core=3 rx=70i,tx=30i,core=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
		fmt.Sprintf(`network,host=server08,region=east,core=4 rx=90i,tx=10i,core=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		fmt.Sprintf(`network,host=server09,region=east,core=1 rx=5i,tx=4i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:20Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "baseline",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM network`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","core","core_1","host","region","rx","tx"],"values":[["2000-01-01T00:00:00Z",2,"1","server01","west",10,20],["2000-01-01T00:00:10Z",3,"2","server02","west",40,50],["2000-01-01T00:00:20Z",4,"3","server03","east",40,55],["2000-01-01T00:00:30Z",1,"4","server04","east",40,60],["2000-01-01T00:00:40Z",2,"1","server05","west",50,70],["2000-01-01T00:00:50Z",3,"2","server06","east",50,40],["2000-01-01T00:01:00Z",4,"3","server07","west",70,30],["2000-01-01T00:01:10Z",1,"4","server08","east",90,10],["2000-01-01T00:01:20Z",2,"1","server09","east",5,4]]}]}]}`,
		},
		{
			name:    "max - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",90]]}]}]}`,
		},
		{
			name:    "max - baseline 30s - epoch ms",
			params:  url.Values{"db": []string{"db0"}, "epoch": []string{"ms"}},
			command: `SELECT max(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp: fmt.Sprintf(
				`{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max"],"values":[[%d,40],[%d,50],[%d,90]]}]}]}`,
				mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()/int64(time.Millisecond),
				mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()/int64(time.Millisecond),
				mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()/int64(time.Millisecond),
			),
		},
		{
			name:    "max - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, max(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","max"],"values":[["2000-01-01T00:00:00Z",50,40],["2000-01-01T00:00:30Z",70,50],["2000-01-01T00:01:00Z",10,90]]}]}]}`,
		},
		{
			name:    "max - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, max(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",90]]}]}]}`,
		},
		{
			name:    "max - time and tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, tx, max(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","max"],"values":[["2000-01-01T00:00:00Z",50,40],["2000-01-01T00:00:30Z",70,50],["2000-01-01T00:01:00Z",10,90]]}]}]}`,
		},
		{
			name:    "min - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","min"],"values":[["2000-01-01T00:00:00Z",10],["2000-01-01T00:00:30Z",40],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "min - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, min(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","min"],"values":[["2000-01-01T00:00:00Z",20,10],["2000-01-01T00:00:30Z",60,40],["2000-01-01T00:01:00Z",4,5]]}]}]}`,
		},
		{
			name:    "min - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, min(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","min"],"values":[["2000-01-01T00:00:00Z",10],["2000-01-01T00:00:30Z",40],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "min - time and tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, tx, min(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","min"],"values":[["2000-01-01T00:00:00Z",20,10],["2000-01-01T00:00:30Z",60,40],["2000-01-01T00:01:00Z",4,5]]}]}]}`,
		},
		{
			name:    "max,min - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(rx), min(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max","min"],"values":[["2000-01-01T00:00:00Z",40,10],["2000-01-01T00:00:30Z",50,40],["2000-01-01T00:01:00Z",90,5]]}]}]}`,
		},
		{
			name:    "first - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT first(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",10],["2000-01-01T00:00:30Z",40],["2000-01-01T00:01:00Z",70]]}]}]}`,
		},
		{
			name:    "first - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, tx, first(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","first"],"values":[["2000-01-01T00:00:00Z",20,10],["2000-01-01T00:00:30Z",60,40],["2000-01-01T00:01:00Z",30,70]]}]}]}`,
		},
		{
			name:    "first - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, first(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",10],["2000-01-01T00:00:30Z",40],["2000-01-01T00:01:00Z",70]]}]}]}`,
		},
		{
			name:    "first - time and tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, tx, first(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","first"],"values":[["2000-01-01T00:00:00Z",20,10],["2000-01-01T00:00:30Z",60,40],["2000-01-01T00:01:00Z",30,70]]}]}]}`,
		},
		{
			name:    "last - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT last(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","last"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "last - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, last(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","last"],"values":[["2000-01-01T00:00:00Z",55,40],["2000-01-01T00:00:30Z",40,50],["2000-01-01T00:01:00Z",4,5]]}]}]}`,
		},
		{
			name:    "last - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, last(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","last"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "last - time and tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, tx, last(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","last"],"values":[["2000-01-01T00:00:00Z",55,40],["2000-01-01T00:00:30Z",40,50],["2000-01-01T00:01:00Z",4,5]]}]}]}`,
		},
		{
			name:    "count - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT count(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","count"],"values":[["2000-01-01T00:00:00Z",3],["2000-01-01T00:00:30Z",3],["2000-01-01T00:01:00Z",3]]}]}]}`,
		},
		{
			name:    "count - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, count(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","count"],"values":[["2000-01-01T00:00:00Z",3],["2000-01-01T00:00:30Z",3],["2000-01-01T00:01:00Z",3]]}]}]}`,
		},
		{
			name:    "count - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, count(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"mixing aggregate and non-aggregate queries is not supported"}]}`,
		},
		{
			name:    "distinct - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT distinct(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","distinct"],"values":[["2000-01-01T00:00:00Z",10],["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",70],["2000-01-01T00:01:00Z",90],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "distinct - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, distinct(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","distinct"],"values":[["2000-01-01T00:00:00Z",10],["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",70],["2000-01-01T00:01:00Z",90],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "distinct - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, distinct(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"aggregate function distinct() cannot be combined with other functions or fields"}]}`,
		},
		{
			name:    "mean - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",30],["2000-01-01T00:00:30Z",46.666666666666664],["2000-01-01T00:01:00Z",55]]}]}]}`,
		},
		{
			name:    "mean - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, mean(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",30],["2000-01-01T00:00:30Z",46.666666666666664],["2000-01-01T00:01:00Z",55]]}]}]}`,
		},
		{
			name:    "mean - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, mean(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"mixing aggregate and non-aggregate queries is not supported"}]}`,
		},
		{
			name:    "median - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT median(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","median"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",70]]}]}]}`,
		},
		{
			name:    "median - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, median(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","median"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",70]]}]}]}`,
		},
		{
			name:    "median - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, median(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"mixing aggregate and non-aggregate queries is not supported"}]}`,
		},
		{
			name:    "mode - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mode(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","mode"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "mode - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, mode(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","mode"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",5]]}]}]}`,
		},
		{
			name:    "mode - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, mode(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"mixing aggregate and non-aggregate queries is not supported"}]}`,
		},
		{
			name:    "spread - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT spread(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","spread"],"values":[["2000-01-01T00:00:00Z",30],["2000-01-01T00:00:30Z",10],["2000-01-01T00:01:00Z",85]]}]}]}`,
		},
		{
			name:    "spread - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, spread(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","spread"],"values":[["2000-01-01T00:00:00Z",30],["2000-01-01T00:00:30Z",10],["2000-01-01T00:01:00Z",85]]}]}]}`,
		},
		{
			name:    "spread - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, spread(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"mixing aggregate and non-aggregate queries is not supported"}]}`,
		},
		{
			name:    "stddev - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT stddev(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","stddev"],"values":[["2000-01-01T00:00:00Z",17.320508075688775],["2000-01-01T00:00:30Z",5.773502691896258],["2000-01-01T00:01:00Z",44.44097208657794]]}]}]}`,
		},
		{
			name:    "stddev - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, stddev(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","stddev"],"values":[["2000-01-01T00:00:00Z",17.320508075688775],["2000-01-01T00:00:30Z",5.773502691896258],["2000-01-01T00:01:00Z",44.44097208657794]]}]}]}`,
		},
		{
			name:    "stddev - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, stddev(rx) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"error":"mixing aggregate and non-aggregate queries is not supported"}]}`,
		},
		{
			name:    "percentile - baseline 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT percentile(rx, 75) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","percentile"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",70]]}]}]}`,
		},
		{
			name:    "percentile - time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, percentile(rx, 75) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","percentile"],"values":[["2000-01-01T00:00:00Z",40],["2000-01-01T00:00:30Z",50],["2000-01-01T00:01:00Z",70]]}]}]}`,
		},
		{
			name:    "percentile - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT tx, percentile(rx, 75) FROM network where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:29Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","tx","percentile"],"values":[["2000-01-01T00:00:00Z",50,40],["2000-01-01T00:00:30Z",70,50],["2000-01-01T00:01:00Z",30,70]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Selectors(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`network,host=server01,region=west,core=1 rx=10i,tx=20i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`network,host=server02,region=west,core=2 rx=40i,tx=50i,core=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`network,host=server03,region=east,core=3 rx=40i,tx=55i,core=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		fmt.Sprintf(`network,host=server04,region=east,core=4 rx=40i,tx=60i,core=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
		fmt.Sprintf(`network,host=server05,region=west,core=1 rx=50i,tx=70i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
		fmt.Sprintf(`network,host=server06,region=east,core=2 rx=50i,tx=40i,core=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
		fmt.Sprintf(`network,host=server07,region=west,core=3 rx=70i,tx=30i,core=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
		fmt.Sprintf(`network,host=server08,region=east,core=4 rx=90i,tx=10i,core=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		fmt.Sprintf(`network,host=server09,region=east,core=1 rx=5i,tx=4i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:20Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "max - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(tx) FROM network`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max"],"values":[["2000-01-01T00:00:40Z",70]]}]}]}`,
		},
		{
			name:    "min - tx",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(tx) FROM network`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","min"],"values":[["2000-01-01T00:01:20Z",4]]}]}]}`,
		},
		{
			name:    "first",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT first(tx) FROM network`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",20]]}]}]}`,
		},
		{
			name:    "last",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT last(tx) FROM network`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","last"],"values":[["2000-01-01T00:01:20Z",4]]}]}]}`,
		},
		{
			name:    "percentile",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT percentile(tx, 50) FROM network`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","percentile"],"values":[["2000-01-01T00:00:50Z",40]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_TopBottomInt(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		// cpu data with overlapping duplicate values
		// hour 0
		fmt.Sprintf(`cpu,host=server01 value=2.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02 value=3.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server03 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		// hour 1
		fmt.Sprintf(`cpu,host=server04 value=3.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server05 value=7.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:10Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server06 value=6.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:20Z").UnixNano()),
		// hour 2
		fmt.Sprintf(`cpu,host=server07 value=7.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T02:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server08 value=9.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T02:00:10Z").UnixNano()),

		// memory data
		// hour 0
		fmt.Sprintf(`memory,host=a,service=redis value=1000i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`memory,host=b,service=mysql value=2000i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`memory,host=b,service=redis value=1500i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		// hour 1
		fmt.Sprintf(`memory,host=a,service=redis value=1001i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:00Z").UnixNano()),
		fmt.Sprintf(`memory,host=b,service=mysql value=2001i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:00Z").UnixNano()),
		fmt.Sprintf(`memory,host=b,service=redis value=1501i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T01:00:00Z").UnixNano()),
		// hour 2
		fmt.Sprintf(`memory,host=a,service=redis value=1002i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T02:00:00Z").UnixNano()),
		fmt.Sprintf(`memory,host=b,service=mysql value=2002i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T02:00:00Z").UnixNano()),
		fmt.Sprintf(`memory,host=b,service=redis value=1502i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T02:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "top - cpu",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 1) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top"],"values":[["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "bottom - cpu",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 1) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "top - cpu - 2 values",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 2) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top"],"values":[["2000-01-01T01:00:10Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "bottom - cpu - 2 values",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 2) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:10Z",3]]}]}]}`,
		},
		{
			name:    "top - cpu - 3 values - sorts on tie properly",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 3) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top"],"values":[["2000-01-01T01:00:10Z",7],["2000-01-01T02:00:00Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "bottom - cpu - 3 values - sorts on tie properly",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 3) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:10Z",3],["2000-01-01T01:00:00Z",3]]}]}]}`,
		},
		{
			name:    "top - cpu - with tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, host, 2) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top","host"],"values":[["2000-01-01T01:00:10Z",7,"server05"],["2000-01-01T02:00:10Z",9,"server08"]]}]}]}`,
		},
		{
			name:    "bottom - cpu - with tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, host, 2) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom","host"],"values":[["2000-01-01T00:00:00Z",2,"server01"],["2000-01-01T00:00:10Z",3,"server02"]]}]}]}`,
		},
		{
			name:    "top - cpu - 3 values with limit 2",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 3) FROM cpu limit 2`,
			exp:     `{"results":[{"statement_id":0,"error":"limit (3) in top function can not be larger than the LIMIT (2) in the select statement"}]}`,
		},
		{
			name:    "bottom - cpu - 3 values with limit 2",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 3) FROM cpu limit 2`,
			exp:     `{"results":[{"statement_id":0,"error":"limit (3) in bottom function can not be larger than the LIMIT (2) in the select statement"}]}`,
		},
		{
			name:    "top - cpu - hourly",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 1) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:10Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top"],"values":[["2000-01-01T00:00:20Z",4],["2000-01-01T01:00:10Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "bottom - cpu - hourly",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 1) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:10Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T01:00:00Z",3],["2000-01-01T02:00:00Z",7]]}]}]}`,
		},
		{
			name:    "top - cpu - 2 values hourly",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 2) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:10Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top"],"values":[["2000-01-01T00:00:10Z",3],["2000-01-01T00:00:20Z",4],["2000-01-01T01:00:10Z",7],["2000-01-01T01:00:20Z",6],["2000-01-01T02:00:00Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "bottom - cpu - 2 values hourly",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 2) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:10Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:10Z",3],["2000-01-01T01:00:00Z",3],["2000-01-01T01:00:20Z",6],["2000-01-01T02:00:00Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "top - cpu - 3 values hourly - validates that a bucket can have less than limit if no values exist in that time bucket",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 3) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:10Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:10Z",3],["2000-01-01T00:00:20Z",4],["2000-01-01T01:00:00Z",3],["2000-01-01T01:00:10Z",7],["2000-01-01T01:00:20Z",6],["2000-01-01T02:00:00Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "bottom - cpu - 3 values hourly - validates that a bucket can have less than limit if no values exist in that time bucket",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 3) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T02:00:10Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:10Z",3],["2000-01-01T00:00:20Z",4],["2000-01-01T01:00:00Z",3],["2000-01-01T01:00:10Z",7],["2000-01-01T01:00:20Z",6],["2000-01-01T02:00:00Z",7],["2000-01-01T02:00:10Z",9]]}]}]}`,
		},
		{
			name:    "top - memory - 2 values, two tags",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, 2), host, service FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","host","service"],"values":[["2000-01-01T01:00:00Z",2001,"b","mysql"],["2000-01-01T02:00:00Z",2002,"b","mysql"]]}]}]}`,
		},
		{
			name:    "bottom - memory - 2 values, two tags",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, 2), host, service FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","host","service"],"values":[["2000-01-01T00:00:00Z",1000,"a","redis"],["2000-01-01T01:00:00Z",1001,"a","redis"]]}]}]}`,
		},
		{
			name:    "top - memory - host tag with limit 2",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, host, 2) FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","host"],"values":[["2000-01-01T02:00:00Z",2002,"b"],["2000-01-01T02:00:00Z",1002,"a"]]}]}]}`,
		},
		{
			name:    "bottom - memory - host tag with limit 2",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, host, 2) FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","host"],"values":[["2000-01-01T00:00:00Z",1000,"a"],["2000-01-01T00:00:00Z",1500,"b"]]}]}]}`,
		},
		{
			name:    "top - memory - host tag with limit 2, service tag in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, host, 2), service FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","host","service"],"values":[["2000-01-01T02:00:00Z",2002,"b","mysql"],["2000-01-01T02:00:00Z",1002,"a","redis"]]}]}]}`,
		},
		{
			name:    "bottom - memory - host tag with limit 2, service tag in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, host, 2), service FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","host","service"],"values":[["2000-01-01T00:00:00Z",1000,"a","redis"],["2000-01-01T00:00:00Z",1500,"b","redis"]]}]}]}`,
		},
		{
			name:    "top - memory - service tag with limit 2, host tag in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, service, 2), host FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","service","host"],"values":[["2000-01-01T02:00:00Z",2002,"mysql","b"],["2000-01-01T02:00:00Z",1502,"redis","b"]]}]}]}`,
		},
		{
			name:    "bottom - memory - service tag with limit 2, host tag in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, service, 2), host FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","service","host"],"values":[["2000-01-01T00:00:00Z",1000,"redis","a"],["2000-01-01T00:00:00Z",2000,"mysql","b"]]}]}]}`,
		},
		{
			name:    "top - memory - host and service tag with limit 2",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, host, service, 2) FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","host","service"],"values":[["2000-01-01T02:00:00Z",2002,"b","mysql"],["2000-01-01T02:00:00Z",1502,"b","redis"]]}]}]}`,
		},
		{
			name:    "bottom - memory - host and service tag with limit 2",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, host, service, 2) FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","host","service"],"values":[["2000-01-01T00:00:00Z",1000,"a","redis"],["2000-01-01T00:00:00Z",1500,"b","redis"]]}]}]}`,
		},
		{
			name:    "top - memory - host tag with limit 2 with service tag in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, host, 2), service FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","host","service"],"values":[["2000-01-01T02:00:00Z",2002,"b","mysql"],["2000-01-01T02:00:00Z",1002,"a","redis"]]}]}]}`,
		},
		{
			name:    "bottom - memory - host tag with limit 2 with service tag in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, host, 2), service FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","host","service"],"values":[["2000-01-01T00:00:00Z",1000,"a","redis"],["2000-01-01T00:00:00Z",1500,"b","redis"]]}]}]}`,
		},
		{
			name:    "top - memory - host and service tag with limit 3",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT TOP(value, host, service, 3) FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","top","host","service"],"values":[["2000-01-01T02:00:00Z",2002,"b","mysql"],["2000-01-01T02:00:00Z",1502,"b","redis"],["2000-01-01T02:00:00Z",1002,"a","redis"]]}]}]}`,
		},
		{
			name:    "bottom - memory - host and service tag with limit 3",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT BOTTOM(value, host, service, 3) FROM memory`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"memory","columns":["time","bottom","host","service"],"values":[["2000-01-01T00:00:00Z",1000,"a","redis"],["2000-01-01T00:00:00Z",1500,"b","redis"],["2000-01-01T00:00:00Z",2000,"b","mysql"]]}]}]}`,
		},

		// TODO
		// - Test that specifiying fields or tags in the function will rewrite the query to expand them to the fields
		// - Test that a field can be used in the top function
		// - Test that asking for a field will come back before a tag if they have the same name for a tag and a field
		// - Test that `select top(value, host, 2)` when there is only one value for `host` it will only bring back one value
		// - Test that `select top(value, host, 4) from foo where time > now() - 1d and time < now() group by time(1h)` and host is unique in some time buckets that it returns only the unique ones, and not always 4 values

	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ExactTimeRange(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu value=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00.000000000Z").UnixNano()),
		fmt.Sprintf(`cpu value=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00.000000001Z").UnixNano()),
		fmt.Sprintf(`cpu value=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00.000000002Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "query point at exactly one time - rfc3339nano",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu WHERE time = '2000-01-01T00:00:00.000000001Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00.000000001Z",2]]}]}]}`,
		},
		{
			name:    "query point at exactly one time - timestamp",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu WHERE time = 946684800000000001`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00.000000001Z",2]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_Math(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`network,host=server01,region=west,core=1 rx=10i,tx=20i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`network,host=server02,region=west,core=2 rx=40i,tx=50i,core=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`network,host=server03,region=east,core=3 rx=40i,tx=55i,core=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		fmt.Sprintf(`network,host=server04,region=east,core=4 rx=40i,tx=60i,core=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
		fmt.Sprintf(`network,host=server05,region=west,core=1 rx=50i,tx=70i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
		fmt.Sprintf(`network,host=server06,region=east,core=2 rx=50i,tx=40i,core=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
		fmt.Sprintf(`network,host=server07,region=west,core=3 rx=70i,tx=30i,core=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
		fmt.Sprintf(`network,host=server08,region=east,core=4 rx=90i,tx=10i,core=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		fmt.Sprintf(`network,host=server09,region=east,core=1 rx=5i,tx=4i,core=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:20Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "add two selectors",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(rx) + min(rx) FROM network WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:01:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max_min"],"values":[["2000-01-01T00:00:00Z",95]]}]}]}`,
		},
		{
			name:    "use math one two selectors separately",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(rx) * 1, min(rx) * 1 FROM network WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:01:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max","min"],"values":[["2000-01-01T00:00:00Z",90,5]]}]}]}`,
		},
		{
			name:    "math with a single selector",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(rx) * 1 FROM network WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:01:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"network","columns":["time","max"],"values":[["2000-01-01T00:01:10Z",90]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_CPU(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`cpu,region=uk,host=serverZ,service=redis value=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
			fmt.Sprintf(`cpu,region=uk,host=serverZ,service=mysql value=30.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "aggregation with WHERE and AND",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) FROM cpu WHERE region='uk' AND host='serverZ'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",50]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_IntMany(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`intmany,host=server01 value=2.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server02 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server03 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server04 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server05 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server06 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server07 value=7.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server08 value=9.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "mean and stddev - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEAN(value), STDDEV(value) FROM intmany WHERE time >= '2000-01-01' AND time < '2000-01-01T00:02:00Z' GROUP BY time(10m)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","mean","stddev"],"values":[["2000-01-01T00:00:00Z",5,2.138089935299395]]}]}]}`,
		},
		{
			name:    "first - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT FIRST(value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "first - int - epoch ms",
			params:  url.Values{"db": []string{"db0"}, "epoch": []string{"ms"}},
			command: `SELECT FIRST(value) FROM intmany`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","first"],"values":[[%d,2]]}]}]}`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()/int64(time.Millisecond)),
		},
		{
			name:    "last - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT LAST(value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","last"],"values":[["2000-01-01T00:01:10Z",9]]}]}]}`,
		},
		{
			name:    "spread - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SPREAD(value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","spread"],"values":[["1970-01-01T00:00:00Z",7]]}]}]}`,
		},
		{
			name:    "median - even count - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEDIAN(value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","median"],"values":[["1970-01-01T00:00:00Z",4.5]]}]}]}`,
		},
		{
			name:    "median - odd count - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEDIAN(value) FROM intmany where time < '2000-01-01T00:01:10Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","median"],"values":[["1970-01-01T00:00:00Z",4]]}]}]}`,
		},
		{
			name:    "mode - single - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MODE(value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","mode"],"values":[["1970-01-01T00:00:00Z",4]]}]}]}`,
		},
		{
			name:    "mode - multiple - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MODE(value) FROM intmany where time < '2000-01-01T00:01:10Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","mode"],"values":[["1970-01-01T00:00:00Z",4]]}]}]}`,
		},
		{
			name:    "distinct as call - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT(value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","distinct"],"values":[["1970-01-01T00:00:00Z",2],["1970-01-01T00:00:00Z",4],["1970-01-01T00:00:00Z",5],["1970-01-01T00:00:00Z",7],["1970-01-01T00:00:00Z",9]]}]}]}`,
		},
		{
			name:    "distinct alt syntax - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT value FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","distinct"],"values":[["1970-01-01T00:00:00Z",2],["1970-01-01T00:00:00Z",4],["1970-01-01T00:00:00Z",5],["1970-01-01T00:00:00Z",7],["1970-01-01T00:00:00Z",9]]}]}]}`,
		},
		{
			name:    "distinct select tag - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT(host) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"error":"statement must have at least one field in select clause"}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): should be allowed, need to stream tag values
		},
		{
			name:    "distinct alt select tag - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT host FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"error":"statement must have at least one field in select clause"}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): should be allowed, need to stream tag values
		},
		{
			name:    "count distinct - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT value) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",5]]}]}]}`,
		},
		{
			name:    "count distinct as call - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT(value)) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",5]]}]}]}`,
		},
		{
			name:    "count distinct select tag - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT host) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",0]]}]}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): stream tag values
		},
		{
			name:    "count distinct as call select tag - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT host) FROM intmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",0]]}]}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): stream tag values
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Where_Fields(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu alert_id="alert",tenant_id="tenant",_cust="johnson brothers" %d`, mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),
		fmt.Sprintf(`cpu alert_id="alert",tenant_id="tenant",_cust="johnson brothers" %d`, mustParseTime(time.RFC3339Nano, "2015-02-28T01:03:36.703820946Z").UnixNano()),

		fmt.Sprintf(`cpu load=100.0,core=4 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu load=80.0,core=2 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:01:02Z").UnixNano()),

		fmt.Sprintf(`clicks local=true %d`, mustParseTime(time.RFC3339Nano, "2014-11-10T23:00:01Z").UnixNano()),
		fmt.Sprintf(`clicks local=false %d`, mustParseTime(time.RFC3339Nano, "2014-11-10T23:00:02Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		// non type specific
		{
			name:    "missing measurement with group by",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT load from missing group by *`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},

		// string
		{
			name:    "single string field",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT alert_id FROM cpu WHERE alert_id='alert'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","alert_id"],"values":[["2015-02-28T01:03:36.703820946Z","alert"]]}]}]}`,
		},
		{
			name:    "string AND query, all fields in SELECT",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT alert_id,tenant_id,_cust FROM cpu WHERE alert_id='alert' AND tenant_id='tenant'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","alert_id","tenant_id","_cust"],"values":[["2015-02-28T01:03:36.703820946Z","alert","tenant","johnson brothers"]]}]}]}`,
		},
		{
			name:    "string AND query, all fields in SELECT, one in parenthesis",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT alert_id,tenant_id FROM cpu WHERE alert_id='alert' AND (tenant_id='tenant')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","alert_id","tenant_id"],"values":[["2015-02-28T01:03:36.703820946Z","alert","tenant"]]}]}]}`,
		},
		{
			name:    "string underscored field",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT alert_id FROM cpu WHERE _cust='johnson brothers'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","alert_id"],"values":[["2015-02-28T01:03:36.703820946Z","alert"]]}]}]}`,
		},
		{
			name:    "string no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT alert_id FROM cpu WHERE _cust='acme'`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},

		// float64
		{
			name:    "float64 GT no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load > 100`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "float64 GTE match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load >= 100`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2009-11-10T23:00:02Z",100]]}]}]}`,
		},
		{
			name:    "float64 EQ match upper bound",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load = 100`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2009-11-10T23:00:02Z",100]]}]}]}`,
		},
		{
			name:    "float64 LTE match two",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load <= 100`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2009-11-10T23:00:02Z",100],["2009-11-10T23:01:02Z",80]]}]}]}`,
		},
		{
			name:    "float64 GT match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load > 99`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2009-11-10T23:00:02Z",100]]}]}]}`,
		},
		{
			name:    "float64 EQ no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load = 99`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "float64 LT match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load < 99`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2009-11-10T23:01:02Z",80]]}]}]}`,
		},
		{
			name:    "float64 LT no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load < 80`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "float64 NE match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select load from cpu where load != 100`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2009-11-10T23:01:02Z",80]]}]}]}`,
		},

		// int64
		{
			name:    "int64 GT no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core > 4`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "int64 GTE match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core >= 4`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2009-11-10T23:00:02Z",4]]}]}]}`,
		},
		{
			name:    "int64 EQ match upper bound",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core = 4`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2009-11-10T23:00:02Z",4]]}]}]}`,
		},
		{
			name:    "int64 LTE match two ",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core <= 4`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2009-11-10T23:00:02Z",4],["2009-11-10T23:01:02Z",2]]}]}]}`,
		},
		{
			name:    "int64 GT match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core > 3`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2009-11-10T23:00:02Z",4]]}]}]}`,
		},
		{
			name:    "int64 EQ no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core = 3`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "int64 LT match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core < 3`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2009-11-10T23:01:02Z",2]]}]}]}`,
		},
		{
			name:    "int64 LT no match",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core < 2`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "int64 NE match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select core from cpu where core != 4`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2009-11-10T23:01:02Z",2]]}]}]}`,
		},

		// bool
		{
			name:    "bool EQ match true",
			params:  url.Values{"db": []string{"db0"}},
			command: `select local from clicks where local = true`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"clicks","columns":["time","local"],"values":[["2014-11-10T23:00:01Z",true]]}]}]}`,
		},
		{
			name:    "bool EQ match false",
			params:  url.Values{"db": []string{"db0"}},
			command: `select local from clicks where local = false`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"clicks","columns":["time","local"],"values":[["2014-11-10T23:00:02Z",false]]}]}]}`,
		},

		{
			name:    "bool NE match one",
			params:  url.Values{"db": []string{"db0"}},
			command: `select local from clicks where local != true`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"clicks","columns":["time","local"],"values":[["2014-11-10T23:00:02Z",false]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_IntMany_GroupBy(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`intmany,host=server01 value=2.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server02 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server03 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server04 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server05 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server06 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server07 value=7.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server08 value=9.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "max order by time with time specified group by 10s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, max(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(10s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:10Z",4],["2000-01-01T00:00:20Z",4],["2000-01-01T00:00:30Z",4],["2000-01-01T00:00:40Z",5],["2000-01-01T00:00:50Z",5],["2000-01-01T00:01:00Z",7],["2000-01-01T00:01:10Z",9]]}]}]}`,
		},
		{
			name:    "max order by time without time specified group by 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",4],["2000-01-01T00:00:30Z",5],["2000-01-01T00:01:00Z",9]]}]}]}`,
		},
		{
			name:    "max order by time with time specified group by 30s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, max(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(30s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",4],["2000-01-01T00:00:30Z",5],["2000-01-01T00:01:00Z",9]]}]}]}`,
		},
		{
			name:    "min order by time without time specified group by 15s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(15s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","min"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:15Z",4],["2000-01-01T00:00:30Z",4],["2000-01-01T00:00:45Z",5],["2000-01-01T00:01:00Z",7]]}]}]}`,
		},
		{
			name:    "min order by time with time specified group by 15s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, min(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(15s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","min"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:15Z",4],["2000-01-01T00:00:30Z",4],["2000-01-01T00:00:45Z",5],["2000-01-01T00:01:00Z",7]]}]}]}`,
		},
		{
			name:    "first order by time without time specified group by 15s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT first(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(15s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:15Z",4],["2000-01-01T00:00:30Z",4],["2000-01-01T00:00:45Z",5],["2000-01-01T00:01:00Z",7]]}]}]}`,
		},
		{
			name:    "first order by time with time specified group by 15s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, first(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(15s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:15Z",4],["2000-01-01T00:00:30Z",4],["2000-01-01T00:00:45Z",5],["2000-01-01T00:01:00Z",7]]}]}]}`,
		},
		{
			name:    "last order by time without time specified group by 15s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT last(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(15s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","last"],"values":[["2000-01-01T00:00:00Z",4],["2000-01-01T00:00:15Z",4],["2000-01-01T00:00:30Z",5],["2000-01-01T00:00:45Z",5],["2000-01-01T00:01:00Z",9]]}]}]}`,
		},
		{
			name:    "last order by time with time specified group by 15s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT time, last(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:14Z' group by time(15s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","last"],"values":[["2000-01-01T00:00:00Z",4],["2000-01-01T00:00:15Z",4],["2000-01-01T00:00:30Z",5],["2000-01-01T00:00:45Z",5],["2000-01-01T00:01:00Z",9]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_IntMany_OrderByDesc(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`intmany,host=server01 value=2.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server02 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server03 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server04 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server05 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server06 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server07 value=7.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
			fmt.Sprintf(`intmany,host=server08 value=9.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "aggregate order by time desc",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(value) FROM intmany where time >= '2000-01-01T00:00:00Z' AND time <= '2000-01-01T00:01:00Z' group by time(10s) order by time desc`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intmany","columns":["time","max"],"values":[["2000-01-01T00:01:00Z",7],["2000-01-01T00:00:50Z",5],["2000-01-01T00:00:40Z",5],["2000-01-01T00:00:30Z",4],["2000-01-01T00:00:20Z",4],["2000-01-01T00:00:10Z",4],["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_IntOverlap(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`intoverlap,region=us-east value=20 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`intoverlap,region=us-east value=30 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`intoverlap,region=us-west value=100 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`intoverlap,region=us-east otherVal=20 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		/*		{
					name:    "aggregation with no interval - int",
					params:  url.Values{"db": []string{"db0"}},
					command: `SELECT count(value) FROM intoverlap WHERE time = '2000-01-01 00:00:00'`,
					exp:     `{"results":[{"statement_id":0,"series":[{"name":"intoverlap","columns":["time","count"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
				},
				{
					name:    "sum - int",
					params:  url.Values{"db": []string{"db0"}},
					command: `SELECT SUM(value) FROM intoverlap WHERE time >= '2000-01-01 00:00:05' AND time <= '2000-01-01T00:00:10Z' GROUP BY time(10s), region`,
					exp:     `{"results":[{"statement_id":0,"series":[{"name":"intoverlap","tags":{"region":"us-east"},"columns":["time","sum"],"values":[["2000-01-01T00:00:10Z",30]]}]}]}`,
				},
		*/{
			name:    "aggregation with a null field value - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM intoverlap GROUP BY region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intoverlap","tags":{"region":"us-east"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",50]]},{"name":"intoverlap","tags":{"region":"us-west"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",100]]}]}]}`,
		},
		{
			name:    "multiple aggregations - int",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value), MEAN(value) FROM intoverlap GROUP BY region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intoverlap","tags":{"region":"us-east"},"columns":["time","sum","mean"],"values":[["1970-01-01T00:00:00Z",50,25]]},{"name":"intoverlap","tags":{"region":"us-west"},"columns":["time","sum","mean"],"values":[["1970-01-01T00:00:00Z",100,100]]}]}]}`,
		},
		{
			skip:    SkippedOSS,
			name:    "multiple aggregations with division - int FIXME issue #2879",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value), mean(value), sum(value) / mean(value) as div FROM intoverlap GROUP BY region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"intoverlap","tags":{"region":"us-east"},"columns":["time","sum","mean","div"],"values":[["1970-01-01T00:00:00Z",50,25,2]]},{"name":"intoverlap","tags":{"region":"us-west"},"columns":["time","div"],"values":[["1970-01-01T00:00:00Z",100,100,1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_FloatSingle(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`floatsingle value=45.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "stddev with just one point - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT STDDEV(value) FROM floatsingle`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatsingle","columns":["time","stddev"],"values":[["1970-01-01T00:00:00Z",null]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_LimitAndOffset(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`limited,tennant=paul foo=2 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`limited,tennant=paul foo=3 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:03Z").UnixNano()),
		fmt.Sprintf(`limited,tennant=paul foo=4 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:04Z").UnixNano()),
		fmt.Sprintf(`limited,tennant=todd foo=5 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:05Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "limit on points",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from "limited" LIMIT 2`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z",2],["2009-11-10T23:00:03Z",3]]}]}]}`,
		},
		{
			name:    "limit higher than the number of data points",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from "limited" LIMIT 20`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z",2],["2009-11-10T23:00:03Z",3],["2009-11-10T23:00:04Z",4],["2009-11-10T23:00:05Z",5]]}]}]}`,
		},
		{
			name:    "limit and offset",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from "limited" LIMIT 2 OFFSET 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","foo"],"values":[["2009-11-10T23:00:03Z",3],["2009-11-10T23:00:04Z",4]]}]}]}`,
		},
		{
			name:    "limit + offset equal to total number of points",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from "limited" LIMIT 3 OFFSET 3`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","foo"],"values":[["2009-11-10T23:00:05Z",5]]}]}]}`,
		},
		{
			name:    "limit - offset higher than number of points",
			command: `select foo from "limited" LIMIT 2 OFFSET 20`,
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit on points with group by time",
			command: `select mean(foo) from "limited" WHERE time >= '2009-11-10T23:00:02Z' AND time < '2009-11-10T23:00:06Z' GROUP BY TIME(1s) LIMIT 2`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","mean"],"values":[["2009-11-10T23:00:02Z",2],["2009-11-10T23:00:03Z",3]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit higher than the number of data points with group by time",
			command: `select mean(foo) from "limited" WHERE time >= '2009-11-10T23:00:02Z' AND time < '2009-11-10T23:00:06Z' GROUP BY TIME(1s) LIMIT 20`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","mean"],"values":[["2009-11-10T23:00:02Z",2],["2009-11-10T23:00:03Z",3],["2009-11-10T23:00:04Z",4],["2009-11-10T23:00:05Z",5]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit and offset with group by time",
			command: `select mean(foo) from "limited" WHERE time >= '2009-11-10T23:00:02Z' AND time < '2009-11-10T23:00:06Z' GROUP BY TIME(1s) LIMIT 2 OFFSET 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","mean"],"values":[["2009-11-10T23:00:03Z",3],["2009-11-10T23:00:04Z",4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit + offset equal to the number of points with group by time",
			command: `select mean(foo) from "limited" WHERE time >= '2009-11-10T23:00:02Z' AND time < '2009-11-10T23:00:06Z' GROUP BY TIME(1s) LIMIT 3 OFFSET 3`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","columns":["time","mean"],"values":[["2009-11-10T23:00:05Z",5]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit - offset higher than number of points with group by time",
			command: `select mean(foo) from "limited" WHERE time >= '2009-11-10T23:00:02Z' AND time < '2009-11-10T23:00:06Z' GROUP BY TIME(1s) LIMIT 2 OFFSET 20`,
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit - group by tennant",
			command: `select foo from "limited" group by tennant limit 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","tags":{"tennant":"paul"},"columns":["time","foo"],"values":[["2009-11-10T23:00:02Z",2]]},{"name":"limited","tags":{"tennant":"todd"},"columns":["time","foo"],"values":[["2009-11-10T23:00:05Z",5]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "limit and offset - group by tennant",
			command: `select foo from "limited" group by tennant limit 1 offset 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"limited","tags":{"tennant":"paul"},"columns":["time","foo"],"values":[["2009-11-10T23:00:03Z",3]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Fill(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`fills val=3 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`fills val=5 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:03Z").UnixNano()),
		fmt.Sprintf(`fills val=4 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:06Z").UnixNano()),
		fmt.Sprintf(`fills val=10 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:16Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "fill with value",
			command: `select mean(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s) FILL(1)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2009-11-10T23:00:00Z",4],["2009-11-10T23:00:05Z",4],["2009-11-10T23:00:10Z",1],["2009-11-10T23:00:15Z",10]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill with value, WHERE all values match condition",
			command: `select mean(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' and val < 50 group by time(5s) FILL(1)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2009-11-10T23:00:00Z",4],["2009-11-10T23:00:05Z",4],["2009-11-10T23:00:10Z",1],["2009-11-10T23:00:15Z",10]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill with value, WHERE no values match condition",
			command: `select mean(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' and val > 50 group by time(5s) FILL(1)`,
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill with previous",
			command: `select mean(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s) FILL(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2009-11-10T23:00:00Z",4],["2009-11-10T23:00:05Z",4],["2009-11-10T23:00:10Z",4],["2009-11-10T23:00:15Z",10]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill with none, i.e. clear out nulls",
			command: `select mean(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s) FILL(none)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2009-11-10T23:00:00Z",4],["2009-11-10T23:00:05Z",4],["2009-11-10T23:00:15Z",10]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill defaults to null",
			command: `select mean(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2009-11-10T23:00:00Z",4],["2009-11-10T23:00:05Z",4],["2009-11-10T23:00:10Z",null],["2009-11-10T23:00:15Z",10]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill defaults to 0 for count",
			command: `select count(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","count"],"values":[["2009-11-10T23:00:00Z",2],["2009-11-10T23:00:05Z",1],["2009-11-10T23:00:10Z",0],["2009-11-10T23:00:15Z",1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill none drops 0s for count",
			command: `select count(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s) fill(none)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","count"],"values":[["2009-11-10T23:00:00Z",2],["2009-11-10T23:00:05Z",1],["2009-11-10T23:00:15Z",1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill previous overwrites 0s for count",
			command: `select count(val) from fills where time >= '2009-11-10T23:00:00Z' and time < '2009-11-10T23:00:20Z' group by time(5s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","count"],"values":[["2009-11-10T23:00:00Z",2],["2009-11-10T23:00:05Z",1],["2009-11-10T23:00:10Z",1],["2009-11-10T23:00:15Z",1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill with implicit start time",
			command: `select mean(val) from fills where time < '2009-11-10T23:00:20Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2009-11-10T23:00:00Z",4],["2009-11-10T23:00:05Z",4],["2009-11-10T23:00:10Z",null],["2009-11-10T23:00:15Z",10]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_FloatMany(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`floatmany,host=server01 value=2.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server02 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server03 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server04 value=4.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server05 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server06 value=5.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:50Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server07 value=7.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
			fmt.Sprintf(`floatmany,host=server08 value=9.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:10Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "mean and stddev - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEAN(value), STDDEV(value) FROM floatmany WHERE time >= '2000-01-01' AND time < '2000-01-01T00:02:00Z' GROUP BY time(10m)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","mean","stddev"],"values":[["2000-01-01T00:00:00Z",5,2.138089935299395]]}]}]}`,
		},
		{
			name:    "first - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT FIRST(value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "last - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT LAST(value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","last"],"values":[["2000-01-01T00:01:10Z",9]]}]}]}`,
		},
		{
			name:    "spread - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SPREAD(value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","spread"],"values":[["1970-01-01T00:00:00Z",7]]}]}]}`,
		},
		{
			name:    "median - even count - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEDIAN(value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","median"],"values":[["1970-01-01T00:00:00Z",4.5]]}]}]}`,
		},
		{
			name:    "median - odd count - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MEDIAN(value) FROM floatmany where time < '2000-01-01T00:01:10Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","median"],"values":[["1970-01-01T00:00:00Z",4]]}]}]}`,
		},
		{
			name:    "mode - single - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MODE(value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","mode"],"values":[["1970-01-01T00:00:00Z",4]]}]}]}`,
		},
		{
			name:    "mode - multiple - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT MODE(value) FROM floatmany where time < '2000-01-01T00:00:10Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","mode"],"values":[["1970-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "distinct as call - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT(value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","distinct"],"values":[["1970-01-01T00:00:00Z",2],["1970-01-01T00:00:00Z",4],["1970-01-01T00:00:00Z",5],["1970-01-01T00:00:00Z",7],["1970-01-01T00:00:00Z",9]]}]}]}`,
		},
		{
			name:    "distinct alt syntax - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT value FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","distinct"],"values":[["1970-01-01T00:00:00Z",2],["1970-01-01T00:00:00Z",4],["1970-01-01T00:00:00Z",5],["1970-01-01T00:00:00Z",7],["1970-01-01T00:00:00Z",9]]}]}]}`,
		},
		{
			name:    "distinct select tag - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT(host) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"error":"statement must have at least one field in select clause"}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): show be allowed, stream tag values
		},
		{
			name:    "distinct alt select tag - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT DISTINCT host FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"error":"statement must have at least one field in select clause"}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): show be allowed, stream tag values
		},
		{
			name:    "count distinct - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT value) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",5]]}]}]}`,
		},
		{
			name:    "count distinct as call - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT(value)) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",5]]}]}]}`,
		},
		{
			name:    "count distinct select tag - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT host) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",0]]}]}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): stream tag values
		},
		{
			name:    "count distinct as call select tag - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT COUNT(DISTINCT host) FROM floatmany`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatmany","columns":["time","count"],"values":[["1970-01-01T00:00:00Z",0]]}]}]}`,
			skip:    SkippedOSS, // FIXME(benbjohnson): stream tag values
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowTagValues(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`_,__name__=metric1 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`_,__name__=metric2 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "show tag values with key",
			command: "SHOW TAG VALUES WITH KEY = host",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"],["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag values with key regex",
			command: "SHOW TAG VALUES WITH KEY =~ /ho/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"],["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY = host WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key regex and where`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY =~ /ho/ WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where matches the regular expression`,
			command: `SHOW TAG VALUES WITH KEY = host WHERE region =~ /ca.*/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where does not match the regular expression`,
			command: `SHOW TAG VALUES WITH KEY = region WHERE host !~ /server0[12]/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["key","value"],"values":[["region","caeast"]]},{"name":"gpu","columns":["key","value"],"values":[["region","caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where partially matches the regular expression`,
			command: `SHOW TAG VALUES WITH KEY = host WHERE region =~ /us/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where partially does not match the regular expression`,
			command: `SHOW TAG VALUES WITH KEY = host WHERE region !~ /us/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]},{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key in and where does not match the regular expression`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY IN (host, region) WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["region","uswest"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key regex and where does not match the regular expression`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY =~ /(host|region)/ WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["region","uswest"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and measurement matches regular expression`,
			command: `SHOW TAG VALUES FROM /[cg]pu/ WITH KEY = host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"],["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag values with key where time",
			command: "SHOW TAG VALUES WITH KEY = host WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"],["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag values with key regex where time",
			command: "SHOW TAG VALUES WITH KEY =~ /ho/ WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"],["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where time`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY = host WHERE region = 'uswest' AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key regex and where time`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY =~ /ho/ WHERE region = 'uswest' AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where matches the regular expression where time`,
			command: `SHOW TAG VALUES WITH KEY = host WHERE region =~ /ca.*/ AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where does not match the regular expression where time`,
			command: `SHOW TAG VALUES WITH KEY = region WHERE host !~ /server0[12]/ AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["key","value"],"values":[["region","caeast"]]},{"name":"gpu","columns":["key","value"],"values":[["region","caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where partially matches the regular expression where time`,
			command: `SHOW TAG VALUES WITH KEY = host WHERE region =~ /us/ AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and where partially does not match the regular expression where time`,
			command: `SHOW TAG VALUES WITH KEY = host WHERE region !~ /us/ AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]},{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key in and where does not match the regular expression where time`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY IN (host, region) WHERE region = 'uswest' AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["region","uswest"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key regex and where does not match the regular expression where time`,
			command: `SHOW TAG VALUES FROM cpu WITH KEY =~ /(host|region)/ WHERE region = 'uswest' AND time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["region","uswest"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key and measurement matches regular expression where time`,
			command: `SHOW TAG VALUES FROM /[cg]pu/ WITH KEY = host WHERE time > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"],["host","server02"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server02"],["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values with key where label starts with underscore`,
			command: `SHOW TAG VALUES FROM "_" WITH KEY = "__name__" WHERE "__name__" = 'metric1'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"_","columns":["key","value"],"values":[["__name__","metric1"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag values with value filter",
			skip:    FixRequired + "https://github.com/influxdata/idpe/issues/7592",
			command: "SHOW TAG VALUES WITH KEY = host WHERE value = 'server03'",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["key","value"],"values":[["host","server03"]]},{"name":"gpu","columns":["key","value"],"values":[["host","server03"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag values with no matching value filter",
			command: "SHOW TAG VALUES WITH KEY = host WHERE value = 'no_such_value'",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag values with non-string value filter",
			command: "SHOW TAG VALUES WITH KEY = host WHERE value = 5000",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowTagKeyCardinality(t *testing.T) {
	t.Skip(NotSupported)
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show tag key cardinality`,
			command: "SHOW TAG KEY CARDINALITY",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"disk","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag key cardinality on db0`,
			command: "SHOW TAG KEY CARDINALITY ON db0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"disk","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
		},
		{
			name:    "show tag key cardinality from",
			command: "SHOW TAG KEY CARDINALITY FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag key cardinality from regex",
			command: "SHOW TAG KEY CARDINALITY FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag key cardinality measurement not found",
			command: "SHOW TAG KEY CARDINALITY FROM doesntexist",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag key cardinality with time in WHERE clause errors",
			command: "SHOW TAG KEY CARDINALITY FROM cpu WHERE time > now() - 1h",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW TAG KEY EXACT CARDINALITY doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag key exact cardinality`,
			command: "SHOW TAG KEY EXACT CARDINALITY",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"disk","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag key exact cardinality on db0`,
			command: "SHOW TAG KEY EXACT CARDINALITY ON db0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"disk","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
		},
		{
			name:    "show tag key exact cardinality from",
			command: "SHOW TAG KEY EXACT CARDINALITY FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag key exact cardinality from regex",
			command: "SHOW TAG KEY EXACT CARDINALITY FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag key exact cardinality measurement not found",
			command: "SHOW TAG KEY EXACT CARDINALITY FROM doesntexist",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag key exact cardinality with time in WHERE clause errors",
			command: "SHOW TAG KEY EXACT CARDINALITY FROM cpu WHERE time > now() - 1h",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW TAG KEY EXACT CARDINALITY doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key and where matches the regular expression`,
			command: `SHOW TAG VALUES CARDINALITY WITH KEY = host WHERE region =~ /ca.*/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key and where does not match the regular expression`,
			command: `SHOW TAG VALUES CARDINALITY WITH KEY = region WHERE host !~ /server0[12]/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key and where partially matches the regular expression`,
			command: `SHOW TAG VALUES CARDINALITY WITH KEY = host WHERE region =~ /us/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key and where partially does not match the regular expression`,
			command: `SHOW TAG VALUES CARDINALITY WITH KEY = host WHERE region !~ /us/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key in and where does not match the regular expression`,
			command: `SHOW TAG VALUES CARDINALITY FROM cpu WITH KEY IN (host, region) WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key regex and where does not match the regular expression`,
			command: `SHOW TAG VALUES CARDINALITY FROM cpu WITH KEY =~ /(host|region)/ WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values cardinality with key and measurement matches regular expression`,
			command: `SHOW TAG VALUES CARDINALITY FROM /[cg]pu/ WITH KEY = host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key and where matches the regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY WITH KEY = host WHERE region =~ /ca.*/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key and where does not match the regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY WITH KEY = region WHERE host !~ /server0[12]/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key and where partially matches the regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY WITH KEY = host WHERE region =~ /us/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key and where partially does not match the regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY WITH KEY = host WHERE region !~ /us/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key in and where does not match the regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY FROM cpu WITH KEY IN (host, region) WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key regex and where does not match the regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY FROM cpu WITH KEY =~ /(host|region)/ WHERE region = 'uswest'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values exact cardinality with key and measurement matches regular expression`,
			command: `SHOW TAG VALUES EXACT CARDINALITY FROM /[cg]pu/ WITH KEY = host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_FloatOverlap(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`floatoverlap,region=us-east value=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`floatoverlap,region=us-east value=30.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`floatoverlap,region=us-west value=100.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`floatoverlap,region=us-east otherVal=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "aggregation with no interval - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT count(value) FROM floatoverlap WHERE time = '2000-01-01 00:00:00'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatoverlap","columns":["time","count"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "sum - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM floatoverlap WHERE time >= '2000-01-01 00:00:05' AND time <= '2000-01-01T00:00:10Z' GROUP BY time(10s), region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatoverlap","tags":{"region":"us-east"},"columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",null],["2000-01-01T00:00:10Z",30]]}]}]}`,
		},
		{
			name:    "aggregation with a null field value - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM floatoverlap GROUP BY region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatoverlap","tags":{"region":"us-east"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",50]]},{"name":"floatoverlap","tags":{"region":"us-west"},"columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",100]]}]}]}`,
		},
		{
			name:    "multiple aggregations - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value), MEAN(value) FROM floatoverlap GROUP BY region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatoverlap","tags":{"region":"us-east"},"columns":["time","sum","mean"],"values":[["1970-01-01T00:00:00Z",50,25]]},{"name":"floatoverlap","tags":{"region":"us-west"},"columns":["time","sum","mean"],"values":[["1970-01-01T00:00:00Z",100,100]]}]}]}`,
		},
		{
			name:    "multiple aggregations with division - float",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) / mean(value) as div FROM floatoverlap GROUP BY region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"floatoverlap","tags":{"region":"us-east"},"columns":["time","div"],"values":[["1970-01-01T00:00:00Z",2]]},{"name":"floatoverlap","tags":{"region":"us-west"},"columns":["time","div"],"values":[["1970-01-01T00:00:00Z",1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowFieldKeyCardinality(t *testing.T) {
	t.Skip(NotSupported)
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 field1=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest field1=200,field2=300,field3=400 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast field1=200,field2=300,field3=400 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast field1=200,field2=300,field3=400 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server01,region=useast field4=200,field5=300 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast field6=200,field7=300 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast field8=200,field9=300 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show field key cardinality`,
			command: `SHOW FIELD KEY CARDINALITY`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[3]]},{"name":"disk","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field key cardinality from measurement`,
			command: `SHOW FIELD KEY CARDINALITY FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[3]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field key cardinality measurement with regex`,
			command: `SHOW FIELD KEY CARDINALITY FROM /[cg]pu/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[3]]},{"name":"gpu","columns":["count"],"values":[[4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field key exact cardinality`,
			command: `SHOW FIELD KEY EXACT CARDINALITY`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[3]]},{"name":"disk","columns":["count"],"values":[[2]]},{"name":"gpu","columns":["count"],"values":[[4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field key exact cardinality from measurement`,
			command: `SHOW FIELD KEY EXACT CARDINALITY FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[3]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field key exact cardinality measurement with regex`,
			command: `SHOW FIELD KEY EXACT CARDINALITY FROM /[cg]pu/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[3]]},{"name":"gpu","columns":["count"],"values":[[4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowFieldKeys(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 field1=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest field1=200,field2=300,field3=400 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast field1=200,field2=300,field3=400 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast field1=200,field2=300,field3=400 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server01,region=useast field4=200,field5=300 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast field6=200,field7=300 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast field8=200,field9=300 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show field keys`,
			command: `SHOW FIELD KEYS`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["fieldKey","fieldType"],"values":[["field1","float"],["field2","float"],["field3","float"]]},{"name":"disk","columns":["fieldKey","fieldType"],"values":[["field8","float"],["field9","float"]]},{"name":"gpu","columns":["fieldKey","fieldType"],"values":[["field4","float"],["field5","float"],["field6","float"],["field7","float"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field keys from measurement`,
			command: `SHOW FIELD KEYS FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["fieldKey","fieldType"],"values":[["field1","float"],["field2","float"],["field3","float"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field keys measurement with regex`,
			command: `SHOW FIELD KEYS FROM /[cg]pu/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["fieldKey","fieldType"],"values":[["field1","float"],["field2","float"],["field3","float"]]},{"name":"gpu","columns":["fieldKey","fieldType"],"values":[["field4","float"],["field5","float"],["field6","float"],["field7","float"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_FieldWithMultiplePeriodsMeasurementPrefixMatch(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`foo foo.bar.baz=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "baseline",
			params:  url.Values{"db": []string{"db0"}},
			command: `select * from foo`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"foo","columns":["time","foo.bar.baz"],"values":[["2000-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "select field with periods",
			params:  url.Values{"db": []string{"db0"}},
			command: `select "foo.bar.baz" from foo`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"foo","columns":["time","foo.bar.baz"],"values":[["2000-01-01T00:00:00Z",1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_FieldWithMultiplePeriods(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu foo.bar.baz=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "baseline",
			params:  url.Values{"db": []string{"db0"}},
			command: `select * from cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","foo.bar.baz"],"values":[["2000-01-01T00:00:00Z",1]]}]}]}`,
		},
		{
			name:    "select field with periods",
			params:  url.Values{"db": []string{"db0"}},
			command: `select "foo.bar.baz" from cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","foo.bar.baz"],"values":[["2000-01-01T00:00:00Z",1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Aggregates_GroupByOffset(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join([]string{
			fmt.Sprintf(`offset,region=us-east,host=serverA value=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
			fmt.Sprintf(`offset,region=us-east,host=serverB value=30.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
			fmt.Sprintf(`offset,region=us-west,host=serverC value=100.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		}, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "group by offset - standard",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) FROM "offset" WHERE time >= '1999-12-31T23:59:55Z' AND time < '2000-01-01T00:00:15Z' GROUP BY time(10s, 5s) FILL(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"offset","columns":["time","sum"],"values":[["1999-12-31T23:59:55Z",120],["2000-01-01T00:00:05Z",30]]}]}]}`,
		},
		{
			name:    "group by offset - misaligned time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) FROM "offset" WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:20Z' GROUP BY time(10s, 5s) FILL(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"offset","columns":["time","sum"],"values":[["1999-12-31T23:59:55Z",120],["2000-01-01T00:00:05Z",30],["2000-01-01T00:00:15Z",0]]}]}]}`,
		},
		{
			name:    "group by offset - negative time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) FROM "offset" WHERE time >= '1999-12-31T23:59:55Z' AND time < '2000-01-01T00:00:15Z' GROUP BY time(10s, -5s) FILL(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"offset","columns":["time","sum"],"values":[["1999-12-31T23:59:55Z",120],["2000-01-01T00:00:05Z",30]]}]}]}`,
		},
		{
			name:    "group by offset - modulo",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) FROM "offset" WHERE time >= '1999-12-31T23:59:55Z' AND time < '2000-01-01T00:00:15Z' GROUP BY time(10s, 35s) FILL(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"offset","columns":["time","sum"],"values":[["1999-12-31T23:59:55Z",120],["2000-01-01T00:00:05Z",30]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// This will test that when using a group by, that it observes the time you asked for
// but will only put the values in the bucket that match the time range
func TestServer_Query_GroupByTimeCutoffs(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu value=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu value=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu value=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:05Z").UnixNano()),
		fmt.Sprintf(`cpu value=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:08Z").UnixNano()),
		fmt.Sprintf(`cpu value=5i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:09Z").UnixNano()),
		fmt.Sprintf(`cpu value=6i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "sum all time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["1970-01-01T00:00:00Z",21]]}]}]}`,
		},
		{
			name:    "sum all time grouped by time 5s",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T00:00:10Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",3],["2000-01-01T00:00:05Z",12],["2000-01-01T00:00:10Z",6]]}]}]}`,
		},
		{
			name:    "sum all time grouped by time 5s missing first point",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM cpu where time >= '2000-01-01T00:00:01Z' and time <= '2000-01-01T00:00:10Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",2],["2000-01-01T00:00:05Z",12],["2000-01-01T00:00:10Z",6]]}]}]}`,
		},
		{
			name:    "sum all time grouped by time 5s missing first points (null for bucket)",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM cpu where time >= '2000-01-01T00:00:02Z' and time <= '2000-01-01T00:00:10Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",null],["2000-01-01T00:00:05Z",12],["2000-01-01T00:00:10Z",6]]}]}]}`,
		},
		{
			name:    "sum all time grouped by time 5s missing last point - 2 time intervals",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T00:00:09Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",3],["2000-01-01T00:00:05Z",12]]}]}]}`,
		},
		{
			name:    "sum all time grouped by time 5s missing last 2 points - 2 time intervals",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT SUM(value) FROM cpu where time >= '2000-01-01T00:00:00Z' and time <= '2000-01-01T00:00:08Z' group by time(5s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",3],["2000-01-01T00:00:05Z",7]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_MapType(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu value=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu speed=25 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "query value with a single measurement",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "query wildcard with a single measurement",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "query value with multiple measurements",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value FROM cpu, gpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "query wildcard with multiple measurements",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu, gpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","speed","value"],"values":[["2000-01-01T00:00:00Z",null,2]]},{"name":"gpu","columns":["time","speed","value"],"values":[["2000-01-01T00:00:00Z",25,null]]}]}]}`,
		},
		{
			name:    "query value with a regex measurement",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value FROM /[cg]pu/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			name:    "query wildcard with a regex measurement",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM /[cg]pu/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","speed","value"],"values":[["2000-01-01T00:00:00Z",null,2]]},{"name":"gpu","columns":["time","speed","value"],"values":[["2000-01-01T00:00:00Z",25,null]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Subqueries(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 usage_user=70i,usage_system=30i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01 usage_user=45i,usage_system=55i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01 usage_user=23i,usage_system=77i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02 usage_user=11i,usage_system=89i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02 usage_user=28i,usage_system=72i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02 usage_user=12i,usage_system=53i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean FROM (SELECT mean(usage_user) FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",31.5]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value FROM (SELECT mean(usage_user) AS value FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",31.5]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(usage) FROM (SELECT 100 - usage_user AS usage FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",68.5]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT host FROM (SELECT min(usage_user), host FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host"],"values":[["2000-01-01T00:00:00Z","server02"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT host FROM (SELECT min(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host"],"values":[["2000-01-01T00:00:00Z","server02"],["2000-01-01T00:00:20Z","server01"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT host FROM (SELECT min(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' GROUP BY host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","host"],"values":[["2000-01-01T00:00:20Z","server01"]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","host"],"values":[["2000-01-01T00:00:00Z","server02"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(min) FROM (SELECT min(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",17]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(min) FROM (SELECT (min(usage_user)) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",17]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(min), host FROM (SELECT min(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","max","host"],"values":[["2000-01-01T00:00:20Z",23,"server01"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean, host FROM (SELECT mean(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean","host"],"values":[["2000-01-01T00:00:00Z",46,"server01"],["2000-01-01T00:00:00Z",17,"server02"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT host FROM (SELECT mean(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","host"],"values":[["2000-01-01T00:00:00Z","server01"],["2000-01-01T00:00:00Z","server02"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max(usage_system) FROM (SELECT min(usage_user), usage_system FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",89]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(top), host FROM (SELECT top(usage_user, host, 2) FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","min","host"],"values":[["2000-01-01T00:00:10Z",28,"server02"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(top), host FROM (SELECT top(usage_user, 2), host FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","min","host"],"values":[["2000-01-01T00:00:10Z",45,"server01"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT count(host) FROM (SELECT top(usage_user, host, 2) FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","count"],"values":[["2000-01-01T00:00:00Z",2]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(derivative) FROM (SELECT derivative(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",-4.6]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(max) FROM (SELECT 100 - max(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","min"],"values":[["2000-01-01T00:00:00Z",30]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(usage_system) FROM (SELECT max(usage_user), 100 - usage_system FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","min"],"values":[["2000-01-01T00:00:10Z",28]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(value) FROM (SELECT max(usage_user), usage_user - usage_system AS value FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","min"],"values":[["2000-01-01T00:00:10Z",-44]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(value) FROM (SELECT top(usage_user, 2), usage_user - usage_system AS value FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' GROUP BY host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","min"],"values":[["2000-01-01T00:00:10Z",-10]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","min"],"values":[["2000-01-01T00:00:10Z",-44]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT min(value) FROM (SELECT max(usage_user), usage_user - usage_system AS value FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' AND host = 'server01'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","min"],"values":[["2000-01-01T00:00:00Z",40]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value FROM (SELECT max(usage_user), usage_user - usage_system AS value FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' AND value > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",40]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT max FROM (SELECT max(usage_user) FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' AND host = 'server01'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","max"],"values":[["2000-01-01T00:00:00Z",70]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(value) FROM (SELECT max(usage_user), usage_user - usage_system AS value FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' AND value > 0`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",40]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(value) FROM (SELECT max(usage_user), usage_user - usage_system AS value FROM cpu GROUP BY host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z' AND host =~ /server/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",-2]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT top(usage_system, host, 2) FROM (SELECT min(usage_user), usage_system FROM cpu GROUP BY time(20s), host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","top","host"],"values":[["2000-01-01T00:00:00Z",89,"server02"],["2000-01-01T00:00:20Z",77,"server01"]]}]}]}`,
		},
		{
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT bottom(usage_system, host, 2) FROM (SELECT max(usage_user), usage_system FROM cpu GROUP BY time(20s), host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","bottom","host"],"values":[["2000-01-01T00:00:00Z",30,"server01"],["2000-01-01T00:00:20Z",53,"server02"]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_SubqueryWithGroupBy(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01,region=uswest value=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=4i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=uswest value=5i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=uswest value=6i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=uswest value=7i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=uswest value=8i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=9i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=10i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=11i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=12i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=13i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=14i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=15i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=16i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "group by time(2s) - time(2s), host",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(mean) FROM (SELECT mean(value) FROM cpu GROUP BY time(2s), host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:04Z' GROUP BY time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",7.5],["2000-01-01T00:00:02Z",9.5]]}]}]}`,
		},
		{
			name:    "group by time(4s), host - time(2s), host",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(mean) FROM (SELECT mean(value) FROM cpu GROUP BY time(2s), host) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:04Z' GROUP BY time(4s), host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",6.5]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",10.5]]}]}]}`,
		},
		{
			name:    "group by time(2s), host - time(2s), host, region",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(mean) FROM (SELECT mean(value) FROM cpu GROUP BY time(2s), host, region) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:04Z' GROUP BY time(2s), host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":"server01"},"columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",5.5],["2000-01-01T00:00:02Z",7.5]]},{"name":"cpu","tags":{"host":"server02"},"columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",9.5],["2000-01-01T00:00:02Z",11.5]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_SubqueryMath(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf("m0 f2=4,f3=2 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf("m0 f1=5,f3=8 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf("m0 f1=5,f2=3,f3=6 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "SumThreeValues",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum FROM (SELECT f1 + f2 + f3 AS sum FROM m0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"m0","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",null],["2000-01-01T00:00:10Z",null],["2000-01-01T00:00:20Z",14]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_PercentileDerivative(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`counter value=12 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`counter value=34 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`counter value=78 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		fmt.Sprintf(`counter value=89 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),
		fmt.Sprintf(`counter value=101 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:40Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "nth percentile of derivative",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT percentile(derivative, 95) FROM (SELECT derivative(value, 1s) FROM counter) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:50Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"counter","columns":["time","percentile"],"values":[["2000-01-01T00:00:20Z",4.4]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_UnderscoreMeasurement(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`_cpu value=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
	}
	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "select underscore with underscore prefix",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM _cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"_cpu","columns":["time","value"],"values":[["2000-01-01T00:00:00Z",1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Wildcards(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`wildcard,region=us-east value=10 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`wildcard,region=us-east valx=20 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`wildcard,region=us-east value=30,valx=40 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),

		fmt.Sprintf(`wgroup,region=us-east value=10.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`wgroup,region=us-east value=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`wgroup,region=us-west value=30.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),

		fmt.Sprintf(`m1,region=us-east value=10.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`m2,host=server01 field=20.0 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "wildcard",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","region","value","valx"],"values":[["2000-01-01T00:00:00Z","us-east",10,null],["2000-01-01T00:00:10Z","us-east",null,20],["2000-01-01T00:00:20Z","us-east",30,40]]}]}]}`,
		},
		{
			name:    "wildcard with group by",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM wildcard GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","tags":{"region":"us-east"},"columns":["time","value","valx"],"values":[["2000-01-01T00:00:00Z",10,null],["2000-01-01T00:00:10Z",null,20],["2000-01-01T00:00:20Z",30,40]]}]}]}`,
		},
		{
			name:    "GROUP BY queries",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(value) FROM wgroup GROUP BY *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wgroup","tags":{"region":"us-east"},"columns":["time","mean"],"values":[["1970-01-01T00:00:00Z",15]]},{"name":"wgroup","tags":{"region":"us-west"},"columns":["time","mean"],"values":[["1970-01-01T00:00:00Z",30]]}]}]}`,
		},
		{
			name:    "GROUP BY queries with time",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT mean(value) FROM wgroup WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:01:00Z' GROUP BY *,TIME(1m)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wgroup","tags":{"region":"us-east"},"columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",15]]},{"name":"wgroup","tags":{"region":"us-west"},"columns":["time","mean"],"values":[["2000-01-01T00:00:00Z",30]]}]}]}`,
		},
		{
			name:    "wildcard and field in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value, * FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","value","region","value_1","valx"],"values":[["2000-01-01T00:00:00Z",10,"us-east",10,null],["2000-01-01T00:00:10Z",null,"us-east",null,20],["2000-01-01T00:00:20Z",30,"us-east",30,40]]}]}]}`,
		},
		{
			name:    "field and wildcard in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT value, * FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","value","region","value_1","valx"],"values":[["2000-01-01T00:00:00Z",10,"us-east",10,null],["2000-01-01T00:00:10Z",null,"us-east",null,20],["2000-01-01T00:00:20Z",30,"us-east",30,40]]}]}]}`,
		},
		{
			name:    "field and wildcard in group by",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM wildcard GROUP BY region, *`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","tags":{"region":"us-east"},"columns":["time","value","valx"],"values":[["2000-01-01T00:00:00Z",10,null],["2000-01-01T00:00:10Z",null,20],["2000-01-01T00:00:20Z",30,40]]}]}]}`,
		},
		{
			name:    "wildcard and field in group by",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM wildcard GROUP BY *, region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","tags":{"region":"us-east"},"columns":["time","value","valx"],"values":[["2000-01-01T00:00:00Z",10,null],["2000-01-01T00:00:10Z",null,20],["2000-01-01T00:00:20Z",30,40]]}]}]}`,
		},
		{
			name:    "wildcard with multiple measurements",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM m1, m2`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"m1","columns":["time","field","host","region","value"],"values":[["2000-01-01T00:00:00Z",null,null,"us-east",10]]},{"name":"m2","columns":["time","field","host","region","value"],"values":[["2000-01-01T00:00:01Z",20,"server01",null,null]]}]}]}`,
		},
		{
			name:    "wildcard with multiple measurements via regex",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM /^m.*/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"m1","columns":["time","field","host","region","value"],"values":[["2000-01-01T00:00:00Z",null,null,"us-east",10]]},{"name":"m2","columns":["time","field","host","region","value"],"values":[["2000-01-01T00:00:01Z",20,"server01",null,null]]}]}]}`,
		},
		{
			name:    "wildcard with multiple measurements via regex and limit",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM db0../^m.*/ LIMIT 2`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"m1","columns":["time","field","host","region","value"],"values":[["2000-01-01T00:00:00Z",null,null,"us-east",10]]},{"name":"m2","columns":["time","field","host","region","value"],"values":[["2000-01-01T00:00:01Z",20,"server01",null,null]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_WildcardExpansion(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`wildcard,region=us-east,host=A value=10,cpu=80 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`wildcard,region=us-east,host=B value=20,cpu=90 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`wildcard,region=us-west,host=B value=30,cpu=70 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
		fmt.Sprintf(`wildcard,region=us-east,host=A value=40,cpu=60 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:30Z").UnixNano()),

		fmt.Sprintf(`dupnames,region=us-east,day=1 value=10,day=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`dupnames,region=us-east,day=2 value=20,day=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`dupnames,region=us-west,day=3 value=30,day=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "wildcard",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","cpu","host","region","value"],"values":[["2000-01-01T00:00:00Z",80,"A","us-east",10],["2000-01-01T00:00:10Z",90,"B","us-east",20],["2000-01-01T00:00:20Z",70,"B","us-west",30],["2000-01-01T00:00:30Z",60,"A","us-east",40]]}]}]}`,
		},
		{
			name:    "no wildcard in select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT cpu, host, region, value  FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","cpu","host","region","value"],"values":[["2000-01-01T00:00:00Z",80,"A","us-east",10],["2000-01-01T00:00:10Z",90,"B","us-east",20],["2000-01-01T00:00:20Z",70,"B","us-west",30],["2000-01-01T00:00:30Z",60,"A","us-east",40]]}]}]}`,
		},
		{
			name:    "no wildcard in select, preserve column order",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT host, cpu, region, value  FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","host","cpu","region","value"],"values":[["2000-01-01T00:00:00Z","A",80,"us-east",10],["2000-01-01T00:00:10Z","B",90,"us-east",20],["2000-01-01T00:00:20Z","B",70,"us-west",30],["2000-01-01T00:00:30Z","A",60,"us-east",40]]}]}]}`,
		},

		{
			name:    "no wildcard with alias",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT cpu as c, host as h, region, value  FROM wildcard`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"wildcard","columns":["time","c","h","region","value"],"values":[["2000-01-01T00:00:00Z",80,"A","us-east",10],["2000-01-01T00:00:10Z",90,"B","us-east",20],["2000-01-01T00:00:20Z",70,"B","us-west",30],["2000-01-01T00:00:30Z",60,"A","us-east",40]]}]}]}`,
		},
		{
			name:    "duplicate tag and field key",
			command: `SELECT * FROM dupnames`,
			params:  url.Values{"db": []string{"db0"}},
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"dupnames","columns":["time","day","day_1","region","value"],"values":[["2000-01-01T00:00:00Z",3,"1","us-east",10],["2000-01-01T00:00:10Z",2,"2","us-east",20],["2000-01-01T00:00:20Z",1,"3","us-west",30]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowQueries_Future(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, models.MaxNanoTime),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show measurements`,
			command: "SHOW MEASUREMENTS",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series`,
			skip:    NotSupported,
			command: "SHOW SERIES",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag keys`,
			command: "SHOW TAG KEYS FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag values`,
			command: "SHOW TAG VALUES WITH KEY = \"host\"",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["key","value"],"values":[["host","server01"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show field keys`,
			command: "SHOW FIELD KEYS",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["fieldKey","fieldType"],"values":[["value","float"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowSeries(t *testing.T) {
	t.Skip(NotSupported)
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:03Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2020-11-10T23:00:04Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2020-11-10T23:00:05Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:06Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:07Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show series`,
			command: "SHOW SERIES",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"],["cpu,host=server01,region=useast"],["cpu,host=server01,region=uswest"],["cpu,host=server02,region=useast"],["disk,host=server03,region=caeast"],["gpu,host=server02,region=useast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series from measurement`,
			command: "SHOW SERIES FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"],["cpu,host=server01,region=useast"],["cpu,host=server01,region=uswest"],["cpu,host=server02,region=useast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series from regular expression`,
			command: "SHOW SERIES FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"],["cpu,host=server01,region=useast"],["cpu,host=server01,region=uswest"],["cpu,host=server02,region=useast"],["gpu,host=server02,region=useast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series with where tag`,
			command: "SHOW SERIES WHERE region = 'uswest'",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01,region=uswest"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series where tag matches regular expression`,
			command: "SHOW SERIES WHERE region =~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["disk,host=server03,region=caeast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series`,
			command: "SHOW SERIES WHERE host !~ /server0[12]/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["disk,host=server03,region=caeast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series with from and where`,
			command: "SHOW SERIES FROM cpu WHERE region = 'useast'",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01,region=useast"],["cpu,host=server02,region=useast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series with time`,
			command: "SHOW SERIES WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"],["cpu,host=server01,region=useast"],["cpu,host=server01,region=uswest"],["cpu,host=server02,region=useast"],["disk,host=server03,region=caeast"],["gpu,host=server02,region=useast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series from measurement with time`,
			command: "SHOW SERIES FROM cpu WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"],["cpu,host=server01,region=useast"],["cpu,host=server01,region=uswest"],["cpu,host=server02,region=useast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series from regular expression with time`,
			command: "SHOW SERIES FROM /[cg]pu/ WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01"],["cpu,host=server01,region=useast"],["cpu,host=server01,region=uswest"],["cpu,host=server02,region=useast"],["gpu,host=server02,region=useast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series with where tag with time`,
			command: "SHOW SERIES WHERE region = 'uswest' AND time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01,region=uswest"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series where tag matches regular expression with time`,
			command: "SHOW SERIES WHERE region =~ /ca.*/ AND time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["disk,host=server03,region=caeast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series with != regex and time`,
			command: "SHOW SERIES WHERE host !~ /server0[12]/ AND time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["disk,host=server03,region=caeast"],["gpu,host=server03,region=caeast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series with from and where with time`,
			command: "SHOW SERIES FROM cpu WHERE region = 'useast' AND time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["key"],"values":[["cpu,host=server01,region=useast"],["cpu,host=server02,region=useast"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowSeriesCardinalityEstimation(t *testing.T) {
	t.Skip(NotSupported)
	// if testing.Short() || os.Getenv("GORACE") != "" || os.Getenv("APPVEYOR") != "" {
	//   t.Skip("Skipping test in short, race and appveyor mode.")
	// }
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = make(Writes, 0, 10)
	// Add 1,000,000 series.
	for j := 0; j < cap(test.writes); j++ {
		writes := make([]string, 0, 50000)
		for i := 0; i < cap(writes); i++ {
			writes = append(writes, fmt.Sprintf(`cpu,l=%d,h=s%d v=1 %d`, j, i, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:01Z").UnixNano()))
		}
		test.writes = append(test.writes, &Write{data: strings.Join(writes, "\n")})
	}

	// These queries use index sketches to estimate cardinality.
	test.addQueries([]*Query{
		{
			name:    `show series cardinality`,
			command: "SHOW SERIES CARDINALITY",
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality on db0`,
			command: "SHOW SERIES CARDINALITY ON db0",
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowSeriesExactCardinality(t *testing.T) {
	t.Skip(NotSupported)
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:03Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:04Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:05Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:06Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:07Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show series cardinality from measurement`,
			command: "SHOW SERIES CARDINALITY FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality from regular expression`,
			command: "SHOW SERIES CARDINALITY FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[4]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality with where tag`,
			command: "SHOW SERIES CARDINALITY WHERE region = 'uswest'",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality where tag matches regular expression`,
			command: "SHOW SERIES CARDINALITY WHERE region =~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality`,
			command: "SHOW SERIES CARDINALITY WHERE host !~ /server0[12]/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality with from and where`,
			command: "SHOW SERIES CARDINALITY FROM cpu WHERE region = 'useast'",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series cardinality with WHERE time should fail`,
			command: "SHOW SERIES CARDINALITY WHERE time > now() - 1h",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW SERIES EXACT CARDINALITY doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality`,
			command: "SHOW SERIES EXACT CARDINALITY",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[4]]},{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality from measurement`,
			command: "SHOW SERIES EXACT CARDINALITY FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[4]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality from regular expression`,
			command: "SHOW SERIES EXACT CARDINALITY FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[4]]},{"name":"gpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality with where tag`,
			command: "SHOW SERIES EXACT CARDINALITY WHERE region = 'uswest'",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality where tag matches regular expression`,
			command: "SHOW SERIES EXACT CARDINALITY WHERE region =~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality`,
			command: "SHOW SERIES EXACT CARDINALITY WHERE host !~ /server0[12]/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["count"],"values":[[1]]},{"name":"gpu","columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality with from and where`,
			command: "SHOW SERIES EXACT CARDINALITY FROM cpu WHERE region = 'useast'",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show series exact cardinality with WHERE time should fail`,
			command: "SHOW SERIES EXACT CARDINALITY WHERE time > now() - 1h",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW SERIES EXACT CARDINALITY doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowMeasurements(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`other,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	ctx := context.Background()

	client := s.MustNewAdminClient()
	bucket2 := influxdb.Bucket{
		OrgID: s.DefaultOrgID,
		Name:  "b2",
	}
	bucket3 := influxdb.Bucket{
		OrgID: s.DefaultOrgID,
		Name:  "b3",
	}
	bucket4 := influxdb.Bucket{
		OrgID: s.DefaultOrgID,
		Name:  "b4",
	}
	require.NoError(t, client.CreateBucket(ctx, &bucket2))
	require.NoError(t, client.CreateBucket(ctx, &bucket3))
	require.NoError(t, client.CreateBucket(ctx, &bucket4))

	require.NoError(t, client.DBRPMappingService.Create(ctx, &influxdb.DBRPMapping{
		Database:        "databaseEmpty",
		RetentionPolicy: "rp0",
		Default:         false,
		OrganizationID:  s.DefaultOrgID,
		BucketID:        bucket4.ID,
	}))

	require.NoError(t, client.DBRPMappingService.Create(ctx, &influxdb.DBRPMapping{
		Database:        "db0",
		RetentionPolicy: "rp1",
		Default:         false,
		OrganizationID:  s.DefaultOrgID,
		BucketID:        bucket2.ID,
	}))

	require.NoError(t, client.DBRPMappingService.Create(ctx, &influxdb.DBRPMapping{
		Database:        "db1",
		RetentionPolicy: "rp0",
		Default:         false,
		OrganizationID:  s.DefaultOrgID,
		BucketID:        bucket3.ID,
	}))

	rp1Writes := []string{
		fmt.Sprintf(`other2,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	db1Writes := []string{
		fmt.Sprintf(`cpu,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
		&Write{bucketID: bucket2.ID, data: strings.Join(rp1Writes, "\n")},
		&Write{bucketID: bucket3.ID, data: strings.Join(db1Writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show measurements`,
			command: "SHOW MEASUREMENTS",
			// *unlike* 1.x, InfluxDB 2 shows measurements from the default retention policy when the rp is not specified, not all retention policies
			exp:    `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"],["gpu"],["other"]]}]}]}`,
			params: url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements with rp parameter`,
			command: "SHOW MEASUREMENTS",
			// we ignore the rp parameter for show measurements
			exp:    `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"],["gpu"],["other"]]}]}]}`,
			params: url.Values{"db": []string{"db0"}, "rp": []string{"rp1"}},
		},
		{
			name:    `show measurements with on`,
			command: "SHOW MEASUREMENTS on db0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"],["gpu"],["other"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}, "rp": []string{"rp0"}},
		},
		{
			name:    `show measurements with limit 2`,
			command: "SHOW MEASUREMENTS LIMIT 2",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"],["gpu"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements using WITH`,
			command: "SHOW MEASUREMENTS WITH MEASUREMENT = cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements using WITH and regex`,
			command: "SHOW MEASUREMENTS WITH MEASUREMENT =~ /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"],["gpu"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements using WITH and regex - no matches`,
			command: "SHOW MEASUREMENTS WITH MEASUREMENT =~ /.*zzzzz.*/",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements where tag matches regular expression`,
			command: "SHOW MEASUREMENTS WHERE region =~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["gpu"],["other"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements where tag does not match a regular expression`,
			command: "SHOW MEASUREMENTS WHERE region !~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements with limit 2 and time`,
			command: "SHOW MEASUREMENTS WHERE time > 0 LIMIT 2",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENTS doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements using WITH and time`,
			command: "SHOW MEASUREMENTS WITH MEASUREMENT = cpu WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENTS doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements using WITH and regex and time`,
			command: "SHOW MEASUREMENTS WITH MEASUREMENT =~ /[cg]pu/ WHERE time > 0 ",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENTS doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements using WITH and regex and time - no matches`,
			command: "SHOW MEASUREMENTS WITH MEASUREMENT =~ /.*zzzzz.*/ WHERE time > 0 ",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENTS doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements and time where tag matches regular expression `,
			command: "SHOW MEASUREMENTS WHERE region =~ /ca.*/ AND time > 0",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENTS doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements and time where tag does not match a regular expression`,
			command: "SHOW MEASUREMENTS WHERE region !~ /ca.*/ AND time > 0",
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENTS doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurements bad wildcard`,
			command: "SHOW MEASUREMENTS on *",
			exp:     `{"results":[{"statement_id":0,"error":"query 'SHOW MEASUREMENTS ON *' not supported. use 'ON *.*' or specify a database"}]}`,
			params:  url.Values{"db": []string{"db0"}, "rp": []string{"rp0"}},
		},
		{
			name:    `show measurements bad rp wildcard`,
			command: "SHOW MEASUREMENTS on *.rp0",
			exp:     `{"results":[{"statement_id":0,"error":"query 'SHOW MEASUREMENTS ON *.rp' not supported. use 'ON *.*' or specify a database"}]}`,
			params:  url.Values{"db": []string{"db0"}, "rp": []string{"rp0"}},
		},
		{
			name:    `show measurements on specific rp`,
			command: "SHOW MEASUREMENTS on db0.rp0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name","database","retention policy"],"values":[["cpu","db0","rp0"],["gpu","db0","rp0"],["other","db0","rp0"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}, "rp": []string{"rp0"}},
		},
		{
			name:    `show measurements on all rps`,
			command: "SHOW MEASUREMENTS on db0.*",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name","database","retention policy"],"values":[["cpu","db0","rp0"],["gpu","db0","rp0"],["other","db0","rp0"],["other2","db0","rp1"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}, "rp": []string{"rp0"}},
		},
		{
			name:    `show measurements on all dbs and rps`,
			command: "SHOW MEASUREMENTS on *.*",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name","database","retention policy"],"values":[["cpu","db0","rp0"],["gpu","db0","rp0"],["other","db0","rp0"],["other2","db0","rp1"],["cpu","db1","rp0"],["disk","db1","rp0"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}, "rp": []string{"rp0"}},
		},
	}...)

	test.Run(ctx, t, s)
}

func TestServer_Query_ShowMeasurementCardinalityEstimation(t *testing.T) {
	// This test fails to build. The offending portions have been commented out
	t.Skip(NotSupported)
	// if testing.Short() || os.Getenv("GORACE") != "" || os.Getenv("APPVEYOR") != "" {
	//   t.Skip("Skipping test in short, race and appveyor mode.")
	// }

	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = make(Writes, 0, 10)
	for j := 0; j < cap(test.writes); j++ {
		writes := make([]string, 0, 10000)
		for i := 0; i < cap(writes); i++ {
			writes = append(writes, fmt.Sprintf(`cpu-%d-s%d v=1 %d`, j, i, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:01Z").UnixNano()))
		}
		test.writes = append(test.writes, &Write{data: strings.Join(writes, "\n")})
	}

	// These queries use index sketches to estimate cardinality.
	test.addQueries([]*Query{
		{
			name:    `show measurement cardinality`,
			command: "SHOW MEASUREMENT CARDINALITY",
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement cardinality on db0`,
			command: "SHOW MEASUREMENT CARDINALITY ON db0",
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowMeasurementExactCardinality(t *testing.T) {
	t.Skip(NotSupported)
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`other,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show measurement cardinality using FROM and regex`,
			command: "SHOW MEASUREMENT CARDINALITY FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement cardinality using FROM and regex - no matches`,
			command: "SHOW MEASUREMENT CARDINALITY FROM /.*zzzzz.*/",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement cardinality where tag matches regular expression`,
			command: "SHOW MEASUREMENT CARDINALITY WHERE region =~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement cardinality where tag does not match a regular expression`,
			command: "SHOW MEASUREMENT CARDINALITY WHERE region !~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement cardinality with time in WHERE clauses errors`,
			command: `SHOW MEASUREMENT CARDINALITY WHERE time > now() - 1h`,
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENT EXACT CARDINALITY doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality`,
			command: "SHOW MEASUREMENT EXACT CARDINALITY",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[3]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality using FROM`,
			command: "SHOW MEASUREMENT EXACT CARDINALITY FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[1]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality using FROM and regex`,
			command: "SHOW MEASUREMENT EXACT CARDINALITY FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality using FROM and regex - no matches`,
			command: "SHOW MEASUREMENT EXACT CARDINALITY FROM /.*zzzzz.*/",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality where tag matches regular expression`,
			command: "SHOW MEASUREMENT EXACT CARDINALITY WHERE region =~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality where tag does not match a regular expression`,
			command: "SHOW MEASUREMENT EXACT CARDINALITY WHERE region !~ /ca.*/",
			exp:     `{"results":[{"statement_id":0,"series":[{"columns":["count"],"values":[[2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show measurement exact cardinality with time in WHERE clauses errors`,
			command: `SHOW MEASUREMENT EXACT CARDINALITY WHERE time > now() - 1h`,
			exp:     `{"results":[{"statement_id":0,"error":"SHOW MEASUREMENT EXACT CARDINALITY doesn't support time in WHERE clause"}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ShowTagKeys(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server01 value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=uswest value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server02,region=useast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`gpu,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
		fmt.Sprintf(`disk,host=server03,region=caeast value=100 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `show tag keys`,
			command: "SHOW TAG KEYS",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"disk","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag keys on db0`,
			command: "SHOW TAG KEYS ON db0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"disk","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
		},
		{
			name:    "show tag keys from",
			command: "SHOW TAG KEYS FROM cpu",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag keys from regex",
			command: "SHOW TAG KEYS FROM /[cg]pu/",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag keys measurement not found",
			command: "SHOW TAG KEYS FROM doesntexist",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag keys with time`,
			command: "SHOW TAG KEYS WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"disk","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    `show tag keys on db0 with time`,
			command: "SHOW TAG KEYS ON db0 WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"disk","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
		},
		{
			name:    "show tag keys with time from",
			command: "SHOW TAG KEYS FROM cpu WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag keys with time from regex",
			command: "SHOW TAG KEYS FROM /[cg]pu/ WHERE time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag keys with time where",
			command: "SHOW TAG KEYS WHERE host = 'server03' AND time > 0",
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"disk","columns":["tagKey"],"values":[["host"],["region"]]},{"name":"gpu","columns":["tagKey"],"values":[["host"],["region"]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "show tag keys with time measurement not found",
			command: "SHOW TAG KEYS FROM doesntexist WHERE time > 0",
			exp:     `{"results":[{"statement_id":0}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_LargeTimestamp(t *testing.T) {
	// This test fails to build. The offending portions have been commented out.
	t.Skip(NeedsReview)
	s := OpenServer(t)
	defer s.Close()

	// if _, ok := s.(*RemoteServer); ok {
	//   t.Skip("Skipping.  Cannot restart remote server")
	// }
	//
	writes := []string{
		fmt.Sprintf(`cpu value=100 %d`, models.MaxNanoTime),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    `select value at max nano time`,
			params:  url.Values{"db": []string{"db0"}},
			command: fmt.Sprintf(`SELECT value FROM cpu WHERE time <= %d`, models.MaxNanoTime),
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["` + time.Unix(0, models.MaxNanoTime).UTC().Format(time.RFC3339Nano) + `",100]]}]}]}`,
		},
	}...)

	// if err := test.init(s); err != nil {
	//   t.Fatalf("test init failed: %s", err)
	// }

	// Open a new server with the same configuration file.
	// This is to ensure the meta data was marshaled correctly.
	// s2 := OpenServer((s.(*LocalServer)).Config)
	// defer s2.(*LocalServer).Server.Close()

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_DotProduct(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu a=2,b=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu a=-5,b=8 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:10Z").UnixNano()),
		fmt.Sprintf(`cpu a=9,b=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:20Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "select dot product",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(a_b) FROM (SELECT a * b FROM cpu) WHERE time >= '2000-01-01T00:00:00Z' AND time < '2000-01-01T00:00:30Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum"],"values":[["2000-01-01T00:00:00Z",-7]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure time in where clause is inclusive
func TestServer_WhereTimeInclusive(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu value=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu value=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu value=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "all GTE/LTE",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time >= '2000-01-01T00:00:01Z' and time <= '2000-01-01T00:00:03Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "all GTE",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time >= '2000-01-01T00:00:01Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "all LTE",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time <= '2000-01-01T00:00:03Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "first GTE/LTE",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time >= '2000-01-01T00:00:01Z' and time <= '2000-01-01T00:00:01Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1]]}]}]}`,
		},
		{
			name:    "last GTE/LTE",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time >= '2000-01-01T00:00:03Z' and time <= '2000-01-01T00:00:03Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "before GTE/LTE",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time <= '2000-01-01T00:00:00Z'`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "all GT/LT",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time > '2000-01-01T00:00:00Z' and time < '2000-01-01T00:00:04Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "first GT/LT",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time > '2000-01-01T00:00:00Z' and time < '2000-01-01T00:00:02Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1]]}]}]}`,
		},
		{
			name:    "last GT/LT",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time > '2000-01-01T00:00:02Z' and time < '2000-01-01T00:00:04Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "all GT",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time > '2000-01-01T00:00:00Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
		{
			name:    "all LT",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * from cpu where time < '2000-01-01T00:00:04Z'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:01Z",1],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:03Z",3]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ImplicitEndTime(t *testing.T) {
	t.Skip(FlakyTest)
	s := OpenServer(t)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	past := now.Add(-10 * time.Second)
	future := now.Add(10 * time.Minute)
	writes := []string{
		fmt.Sprintf(`cpu value=1 %d`, past.UnixNano()),
		fmt.Sprintf(`cpu value=2 %d`, future.UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "raw query",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",1],["%s",2]]}]}]}`, past.Format(time.RFC3339Nano), future.Format(time.RFC3339Nano)),
		},
		{
			name:    "aggregate query",
			params:  url.Values{"db": []string{"db0"}},
			command: fmt.Sprintf(`SELECT mean(value) FROM cpu WHERE time > '%s' - 1m GROUP BY time(1m) FILL(none)`, now.Truncate(time.Minute).Format(time.RFC3339Nano)),
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","mean"],"values":[["%s",1]]}]}]}`, now.Truncate(time.Minute).Format(time.RFC3339Nano)),
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Sample_Wildcard(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu float=1,int=1i,string="hello, world",bool=true %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "sample() with wildcard",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sample(*, 1) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sample_bool","sample_float","sample_int","sample_string"],"values":[["2000-01-01T00:00:00Z",true,1,1,"hello, world"]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Sample_LimitOffset(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu float=1,int=1i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu float=2,int=2i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:01:00Z").UnixNano()),
		fmt.Sprintf(`cpu float=3,int=3i %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:02:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "sample() with limit 1",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sample(float, 3), int FROM cpu LIMIT 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sample","int"],"values":[["2000-01-01T00:00:00Z",1,1]]}]}]}`,
		},
		{
			name:    "sample() with offset 1",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sample(float, 3), int FROM cpu OFFSET 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sample","int"],"values":[["2000-01-01T00:01:00Z",2,2],["2000-01-01T00:02:00Z",3,3]]}]}]}`,
		},
		{
			name:    "sample() with limit 1 offset 1",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sample(float, 3), int FROM cpu LIMIT 1 OFFSET 1`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sample","int"],"values":[["2000-01-01T00:01:00Z",2,2]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Validate that nested aggregates don't panic
func TestServer_NestedAggregateWithMathPanics(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		`cpu value=2i 120000000000`,
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "dividing by elapsed count should not panic",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) / elapsed(sum(value), 1m) FROM cpu WHERE time > 0 AND time < 10m GROUP BY time(1m)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum_elapsed"],"values":[["1970-01-01T00:00:00Z",null],["1970-01-01T00:01:00Z",null],["1970-01-01T00:02:00Z",null],["1970-01-01T00:03:00Z",null],["1970-01-01T00:04:00Z",null],["1970-01-01T00:05:00Z",null],["1970-01-01T00:06:00Z",null],["1970-01-01T00:07:00Z",null],["1970-01-01T00:08:00Z",null],["1970-01-01T00:09:00Z",null]]}]}]}`,
		},
		{
			name:    "dividing by elapsed count with fill previous should not panic",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT sum(value) / elapsed(sum(value), 1m) FROM cpu WHERE time > 0 AND time < 10m GROUP BY time(1m) FILL(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","sum_elapsed"],"values":[["1970-01-01T00:00:00Z",null],["1970-01-01T00:01:00Z",null],["1970-01-01T00:02:00Z",null],["1970-01-01T00:03:00Z",2],["1970-01-01T00:04:00Z",2],["1970-01-01T00:05:00Z",2],["1970-01-01T00:06:00Z",2],["1970-01-01T00:07:00Z",2],["1970-01-01T00:08:00Z",2],["1970-01-01T00:09:00Z",2]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server will succeed and error for common scenarios.
func TestServer_Query_Common(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: fmt.Sprintf("cpu,host=server01 value=1 %s", strconv.FormatInt(now.UnixNano(), 10))},
	}

	test.addQueries([]*Query{
		{
			name:    "selecting a from a non-existent database should error",
			command: `SELECT value FROM db1.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"database not found: db1"}]}`,
		},
		{
			name:    "selecting a from a non-existent retention policy should error",
			command: `SELECT value FROM db0.rp1.cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"retention policy not found: rp1"}]}`,
		},
		{
			name:    "selecting a valid  measurement and field should succeed",
			command: `SELECT value FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",1]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "explicitly selecting time and a valid measurement and field should succeed",
			command: `SELECT time,value FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",1]]}]}]}`, now.Format(time.RFC3339Nano)),
		},
		{
			name:    "selecting a measurement that doesn't exist should result in empty set",
			command: `SELECT value FROM db0.rp0.idontexist`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "selecting a field that doesn't exist should result in empty set",
			command: `SELECT idontexist FROM db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "selecting wildcard without specifying a database should error",
			command: `SELECT * FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"database name required"}]}`,
		},
		{
			name:    "selecting explicit field without specifying a database should error",
			command: `SELECT value FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"error":"database name required"}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query two points.
func TestServer_Query_SelectTwoPoints(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: fmt.Sprintf("cpu value=100 %s\ncpu value=200 %s", strconv.FormatInt(now.UnixNano(), 10), strconv.FormatInt(now.Add(1).UnixNano(), 10))},
	}

	test.addQueries(
		&Query{
			name:    "selecting two points should result in two points",
			command: `SELECT * FROM db0.rp0.cpu`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",100],["%s",200]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
		&Query{
			name:    "selecting two points with GROUP BY * should result in two points",
			command: `SELECT * FROM db0.rp0.cpu GROUP BY *`,
			exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",100],["%s",200]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
		},
	)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can query two negative points.
func TestServer_Query_SelectTwoNegativePoints(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	now := now()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: fmt.Sprintf("cpu value=-100 %s\ncpu value=-200 %s", strconv.FormatInt(now.UnixNano(), 10), strconv.FormatInt(now.Add(1).UnixNano(), 10))},
	}

	test.addQueries(&Query{
		name:    "selecting two negative points should succeed",
		command: `SELECT * FROM db0.rp0.cpu`,
		exp:     fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["%s",-100],["%s",-200]]}]}]}`, now.Format(time.RFC3339Nano), now.Add(1).Format(time.RFC3339Nano)),
	})

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various simple derivative queries.
func TestServer_Query_SelectRawDerivative(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: "cpu value=210 1278010021000000000\ncpu value=10 1278010022000000000"},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate single derivate",
			command: `SELECT derivative(value) from db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",-200]]}]}]}`,
		},
		{
			name:    "calculate derivate with unit",
			command: `SELECT derivative(value, 10s) from db0.rp0.cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",-2000]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time derivative queries.
func TestServer_Query_SelectGroupByTimeDerivativeWithFill(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=20 1278010021000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate derivative of count with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",-2]]}]}]}`,
		},
		{
			name:    "calculate derivative of count with unit 4s group by time with fill  0",
			command: `SELECT derivative(count(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",4],["2010-07-01T18:47:02Z",-4]]}]}]}`,
		},
		{
			name:    "calculate derivative of count with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of count with unit 4s group by time with fill previous",
			command: `SELECT derivative(count(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of mean with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",-15]]}]}]}`,
		},
		{
			name:    "calculate derivative of mean with unit 4s group by time with fill 0",
			command: `SELECT derivative(mean(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",30],["2010-07-01T18:47:02Z",-30]]}]}]}`,
		},
		{
			name:    "calculate derivative of mean with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of mean with unit 4s group by time with fill previous",
			command: `SELECT derivative(mean(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of median with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",-15]]}]}]}`,
		},
		{
			name:    "calculate derivative of median with unit 4s group by time with fill 0",
			command: `SELECT derivative(median(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",30],["2010-07-01T18:47:02Z",-30]]}]}]}`,
		},
		{
			name:    "calculate derivative of median with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of median with unit 4s group by time with fill previous",
			command: `SELECT derivative(median(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of mode with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate derivative of mode with unit 4s group by time with fill 0",
			command: `SELECT derivative(mode(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate derivative of mode with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of mode with unit 4s group by time with fill previous",
			command: `SELECT derivative(mode(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of sum with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",30],["2010-07-01T18:47:02Z",-30]]}]}]}`,
		},
		{
			name:    "calculate derivative of sum with unit 4s group by time with fill 0",
			command: `SELECT derivative(sum(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",60],["2010-07-01T18:47:02Z",-60]]}]}]}`,
		},
		{
			name:    "calculate derivative of sum with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of sum with unit 4s group by time with fill previous",
			command: `SELECT derivative(sum(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of first with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate derivative of first with unit 4s group by time with fill 0",
			command: `SELECT derivative(first(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate derivative of first with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of first with unit 4s group by time with fill previous",
			command: `SELECT derivative(first(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of last with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate derivative of last with unit 4s group by time with fill 0",
			command: `SELECT derivative(last(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",40],["2010-07-01T18:47:02Z",-40]]}]}]}`,
		},
		{
			name:    "calculate derivative of last with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of last with unit 4s group by time with fill previous",
			command: `SELECT derivative(last(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of min with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate derivative of min with unit 4s group by time with fill 0",
			command: `SELECT derivative(min(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate derivative of min with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of min with unit 4s group by time with fill previous",
			command: `SELECT derivative(min(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of max with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate derivative of max with unit 4s group by time with fill 0",
			command: `SELECT derivative(max(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",40],["2010-07-01T18:47:02Z",-40]]}]}]}`,
		},
		{
			name:    "calculate derivative of max with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of max with unit 4s group by time with fill previous",
			command: `SELECT derivative(max(value), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of percentile with unit default (2s) group by time with fill 0",
			command: `SELECT derivative(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate derivative of percentile with unit 4s group by time with fill 0",
			command: `SELECT derivative(percentile(value, 50), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate derivative of percentile with unit default (2s) group by time with fill previous",
			command: `SELECT derivative(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate derivative of percentile with unit 4s group by time with fill previous",
			command: `SELECT derivative(percentile(value, 50), 4s) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time difference queries.
func TestServer_Query_SelectGroupByTimeDifference(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=15 1278010021000000000
cpu value=20 1278010022000000000
cpu value=25 1278010023000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate difference of count",
			command: `SELECT difference(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of mean",
			command: `SELECT difference(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of median",
			command: `SELECT difference(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of mode",
			command: `SELECT difference(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of sum",
			command: `SELECT difference(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",20]]}]}]}`,
		},
		{
			name:    "calculate difference of first",
			command: `SELECT difference(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of last",
			command: `SELECT difference(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of min",
			command: `SELECT difference(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of max",
			command: `SELECT difference(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
		{
			name:    "calculate difference of percentile",
			command: `SELECT difference(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",10]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Ensure the server can handle various group by time difference queries with fill.
func TestServer_Query_SelectGroupByTimeDifferenceWithFill(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: `cpu value=10 1278010020000000000
cpu value=20 1278010021000000000
`},
	}

	test.addQueries([]*Query{
		{
			name:    "calculate difference of count with fill 0",
			command: `SELECT difference(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",2],["2010-07-01T18:47:02Z",-2]]}]}]}`,
		},
		{
			name:    "calculate difference of count with fill previous",
			command: `SELECT difference(count(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of mean with fill 0",
			command: `SELECT difference(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",-15]]}]}]}`,
		},
		{
			name:    "calculate difference of mean with fill previous",
			command: `SELECT difference(mean(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of median with fill 0",
			command: `SELECT difference(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",15],["2010-07-01T18:47:02Z",-15]]}]}]}`,
		},
		{
			name:    "calculate difference of median with fill previous",
			command: `SELECT difference(median(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of mode with fill 0",
			command: `SELECT difference(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate difference of mode with fill previous",
			command: `SELECT difference(mode(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of sum with fill 0",
			command: `SELECT difference(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",30],["2010-07-01T18:47:02Z",-30]]}]}]}`,
		},
		{
			name:    "calculate difference of sum with fill previous",
			command: `SELECT difference(sum(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of first with fill 0",
			command: `SELECT difference(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate difference of first with fill previous",
			command: `SELECT difference(first(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of last with fill 0",
			command: `SELECT difference(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate difference of last with fill previous",
			command: `SELECT difference(last(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of min with fill 0",
			command: `SELECT difference(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate difference of min with fill previous",
			command: `SELECT difference(min(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of max with fill 0",
			command: `SELECT difference(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",20],["2010-07-01T18:47:02Z",-20]]}]}]}`,
		},
		{
			name:    "calculate difference of max with fill previous",
			command: `SELECT difference(max(value)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
		{
			name:    "calculate difference of percentile with fill 0",
			command: `SELECT difference(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(0)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:00Z",10],["2010-07-01T18:47:02Z",-10]]}]}]}`,
		},
		{
			name:    "calculate difference of percentile with fill previous",
			command: `SELECT difference(percentile(value, 50)) from db0.rp0.cpu where time >= '2010-07-01 18:47:00' and time <= '2010-07-01 18:47:03' group by time(2s) fill(previous)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-07-01T18:47:02Z",0]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

// Test various aggregates when different series only have data for the same timestamp.
func TestServer_Query_Aggregates_IdenticalTime(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`series,host=a value=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=b value=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=c value=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=d value=4 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=e value=5 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=f value=5 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=g value=5 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=h value=5 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`series,host=i value=5 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "last from multiple series with identical timestamp",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT last(value) FROM "series"`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"series","columns":["time","last"],"values":[["2000-01-01T00:00:00Z",5]]}]}]}`,
			repeat:  100,
		},
		{
			name:    "first from multiple series with identical timestamp",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT first(value) FROM "series"`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"series","columns":["time","first"],"values":[["2000-01-01T00:00:00Z",5]]}]}]}`,
			repeat:  100,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_AcrossShardsAndFields(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu load=100 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu load=200 %d`, mustParseTime(time.RFC3339Nano, "2010-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu core=4 %d`, mustParseTime(time.RFC3339Nano, "2015-01-01T00:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "two results for cpu",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT load FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","load"],"values":[["2000-01-01T00:00:00Z",100],["2010-01-01T00:00:00Z",200]]}]}]}`,
		},
		{
			name:    "two results for cpu, multi-select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT core,load FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core","load"],"values":[["2000-01-01T00:00:00Z",null,100],["2010-01-01T00:00:00Z",null,200],["2015-01-01T00:00:00Z",4,null]]}]}]}`,
		},
		{
			name:    "two results for cpu, wildcard select",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT * FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core","load"],"values":[["2000-01-01T00:00:00Z",null,100],["2010-01-01T00:00:00Z",null,200],["2015-01-01T00:00:00Z",4,null]]}]}]}`,
		},
		{
			name:    "one result for core",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT core FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","core"],"values":[["2015-01-01T00:00:00Z",4]]}]}]}`,
		},
		{
			name:    "empty result set from non-existent field",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT foo FROM cpu`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_OrderedAcrossShards(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu value=7 %d`, mustParseTime(time.RFC3339Nano, "2010-01-01T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu value=14 %d`, mustParseTime(time.RFC3339Nano, "2010-01-08T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu value=28 %d`, mustParseTime(time.RFC3339Nano, "2010-01-15T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu value=56 %d`, mustParseTime(time.RFC3339Nano, "2010-01-22T00:00:00Z").UnixNano()),
		fmt.Sprintf(`cpu value=112 %d`, mustParseTime(time.RFC3339Nano, "2010-01-29T00:00:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "derivative",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT derivative(value, 24h) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","derivative"],"values":[["2010-01-08T00:00:00Z",1],["2010-01-15T00:00:00Z",2],["2010-01-22T00:00:00Z",4],["2010-01-29T00:00:00Z",8]]}]}]}`,
		},
		{
			name:    "non_negative_derivative",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT non_negative_derivative(value, 24h) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","non_negative_derivative"],"values":[["2010-01-08T00:00:00Z",1],["2010-01-15T00:00:00Z",2],["2010-01-22T00:00:00Z",4],["2010-01-29T00:00:00Z",8]]}]}]}`,
		},
		{
			name:    "difference",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT difference(value) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","difference"],"values":[["2010-01-08T00:00:00Z",7],["2010-01-15T00:00:00Z",14],["2010-01-22T00:00:00Z",28],["2010-01-29T00:00:00Z",56]]}]}]}`,
		},
		{
			name:    "cumulative_sum",
			params:  url.Values{"db": []string{"db0"}},
			command: `SELECT cumulative_sum(value) FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","cumulative_sum"],"values":[["2010-01-01T00:00:00Z",7],["2010-01-08T00:00:00Z",21],["2010-01-15T00:00:00Z",49],["2010-01-22T00:00:00Z",105],["2010-01-29T00:00:00Z",217]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_Where_With_Tags(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`where_events,tennant=paul foo="bar" %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`where_events,tennant=paul foo="baz" %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:03Z").UnixNano()),
		fmt.Sprintf(`where_events,tennant=paul foo="bat" %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:04Z").UnixNano()),
		fmt.Sprintf(`where_events,tennant=todd foo="bar" %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:05Z").UnixNano()),
		fmt.Sprintf(`where_events,tennant=david foo="bap" %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:06Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "tag field and time",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from where_events where (tennant = 'paul' OR tennant = 'david') AND time > 1s AND (foo = 'bar' OR foo = 'baz' OR foo = 'bap')`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"where_events","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z","bar"],["2009-11-10T23:00:03Z","baz"],["2009-11-10T23:00:06Z","bap"]]}]}]}`,
		},
		{
			name:    "tag or field",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from where_events where tennant = 'paul' OR foo = 'bar'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"where_events","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z","bar"],["2009-11-10T23:00:03Z","baz"],["2009-11-10T23:00:04Z","bat"],["2009-11-10T23:00:05Z","bar"]]}]}]}`,
		},
		{
			name:    "non-existent tag and field",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from where_events where tenant != 'paul' AND foo = 'bar'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"where_events","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z","bar"],["2009-11-10T23:00:05Z","bar"]]}]}]}`,
		},
		{
			name:    "non-existent tag or field",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from where_events where tenant != 'paul' OR foo = 'bar'`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"where_events","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z","bar"],["2009-11-10T23:00:03Z","baz"],["2009-11-10T23:00:04Z","bat"],["2009-11-10T23:00:05Z","bar"],["2009-11-10T23:00:06Z","bap"]]}]}]}`,
		},
		{
			name:    "where comparing tag and field",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from where_events where tennant != foo`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"where_events","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z","bar"],["2009-11-10T23:00:03Z","baz"],["2009-11-10T23:00:04Z","bat"],["2009-11-10T23:00:05Z","bar"],["2009-11-10T23:00:06Z","bap"]]}]}]}`,
		},
		{
			name:    "where comparing tag and tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `select foo from where_events where tennant = tennant`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"where_events","columns":["time","foo"],"values":[["2009-11-10T23:00:02Z","bar"],["2009-11-10T23:00:03Z","baz"],["2009-11-10T23:00:04Z","bat"],["2009-11-10T23:00:05Z","bar"],["2009-11-10T23:00:06Z","bap"]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_With_EmptyTags(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu value=1 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server01 value=2 %d`, mustParseTime(time.RFC3339Nano, "2009-11-10T23:00:03Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "where empty tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host = ''`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:02Z",1]]}]}]}`,
		},
		{
			name:    "where not empty tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host != ''`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:03Z",2]]}]}]}`,
		},
		{
			name:    "where regex none",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host !~ /.*/`,
			exp:     `{"results":[{"statement_id":0}]}`,
		},
		{
			name:    "where regex exact",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host =~ /^server01$/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:03Z",2]]}]}]}`,
		},
		{
			name:    "where regex exact (case insensitive)",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host =~ /(?i)^SeRvEr01$/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:03Z",2]]}]}]}`,
		},
		{
			name:    "where regex exact (not)",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host !~ /^server01$/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:02Z",1]]}]}]}`,
		},
		{
			name:    "where regex at least one char",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host =~ /.+/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:03Z",2]]}]}]}`,
		},
		{
			name:    "where regex not at least one char",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu where host !~ /.+/`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2009-11-10T23:00:02Z",1]]}]}]}`,
		},
		{
			name:    "group by empty tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu group by host`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"host":""},"columns":["time","value"],"values":[["2009-11-10T23:00:02Z",1]]},{"name":"cpu","tags":{"host":"server01"},"columns":["time","value"],"values":[["2009-11-10T23:00:03Z",2]]}]}]}`,
		},
		{
			name:    "group by missing tag",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from cpu group by region`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","tags":{"region":""},"columns":["time","value"],"values":[["2009-11-10T23:00:02Z",1],["2009-11-10T23:00:03Z",2]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_ImplicitFill(t *testing.T) {
	s := OpenServer(t, func(o *launcher.InfluxdOpts) {
		o.CoordinatorConfig.MaxSelectBucketsN = 5
	})
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`fills val=1 %d`, mustParseTime(time.RFC3339Nano, "2010-01-01T11:30:00Z").UnixNano()),
		fmt.Sprintf(`fills val=3 %d`, mustParseTime(time.RFC3339Nano, "2010-01-01T12:00:00Z").UnixNano()),
		fmt.Sprintf(`fills val=5 %d`, mustParseTime(time.RFC3339Nano, "2010-01-01T16:30:00Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "fill with implicit start",
			command: `select mean(val) from fills where time < '2010-01-01T18:00:00Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2010-01-01T16:00:00Z",5],["2010-01-01T17:00:00Z",null]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
		{
			name:    "fill with implicit start - max select buckets",
			command: `select mean(val) from fills where time < '2010-01-01T17:00:00Z' group by time(1h)`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"fills","columns":["time","mean"],"values":[["2010-01-01T12:00:00Z",3],["2010-01-01T13:00:00Z",null],["2010-01-01T14:00:00Z",null],["2010-01-01T15:00:00Z",null],["2010-01-01T16:00:00Z",5]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_MaxRowLimit(t *testing.T) {
	t.Skip(NotSupported)
	// config := NewConfig()
	// config.HTTPD.MaxRowLimit = 10

	s := OpenServer(t)
	defer s.Close()

	writes := make([]string, 11) // write one extra value beyond the max row limit
	expectedValues := make([]string, 10)
	for i := 0; i < len(writes); i++ {
		writes[i] = fmt.Sprintf(`cpu value=%d %d`, i, time.Unix(0, int64(i)).UnixNano())
		if i < len(expectedValues) {
			expectedValues[i] = fmt.Sprintf(`["%s",%d]`, time.Unix(0, int64(i)).UTC().Format(time.RFC3339Nano), i)
		}
	}
	expected := fmt.Sprintf(`{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[%s],"partial":true}]}]}`, strings.Join(expectedValues, ","))

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "SELECT all values, no chunking",
			command: `SELECT value FROM cpu`,
			exp:     expected,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_EvilIdentifiers(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: fmt.Sprintf("cpu select=1,in-bytes=2 %d", mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:00Z").UnixNano())},
	}

	test.addQueries([]*Query{
		{
			name:    `query evil identifiers`,
			command: `SELECT "select", "in-bytes" FROM cpu`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","select","in-bytes"],"values":[["2000-01-01T00:00:00Z",1,2]]}]}]}`,
			params:  url.Values{"db": []string{"db0"}},
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}

func TestServer_Query_OrderByTime(t *testing.T) {
	s := OpenServer(t)
	defer s.Close()

	writes := []string{
		fmt.Sprintf(`cpu,host=server1 value=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server1 value=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`cpu,host=server1 value=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),

		fmt.Sprintf(`power,presence=true value=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`power,presence=true value=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`power,presence=true value=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:03Z").UnixNano()),
		fmt.Sprintf(`power,presence=false value=4 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:04Z").UnixNano()),

		fmt.Sprintf(`mem,host=server1 free=1 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`mem,host=server1 free=2 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
		fmt.Sprintf(`mem,host=server2 used=3 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:01Z").UnixNano()),
		fmt.Sprintf(`mem,host=server2 used=4 %d`, mustParseTime(time.RFC3339Nano, "2000-01-01T00:00:02Z").UnixNano()),
	}

	test := NewTest("db0", "rp0")
	test.writes = Writes{
		&Write{data: strings.Join(writes, "\n")},
	}

	test.addQueries([]*Query{
		{
			name:    "order on points",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from "cpu" ORDER BY time DESC`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"cpu","columns":["time","value"],"values":[["2000-01-01T00:00:03Z",3],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:01Z",1]]}]}]}`,
		},

		{
			name:    "order desc with tags",
			params:  url.Values{"db": []string{"db0"}},
			command: `select value from "power" ORDER BY time DESC`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"power","columns":["time","value"],"values":[["2000-01-01T00:00:04Z",4],["2000-01-01T00:00:03Z",3],["2000-01-01T00:00:02Z",2],["2000-01-01T00:00:01Z",1]]}]}]}`,
		},

		{
			name:    "order desc with sparse data",
			params:  url.Values{"db": []string{"db0"}},
			command: `select used, free from "mem" ORDER BY time DESC`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"mem","columns":["time","used","free"],"values":[["2000-01-01T00:00:02Z",4,null],["2000-01-01T00:00:02Z",null,2],["2000-01-01T00:00:01Z",3,null],["2000-01-01T00:00:01Z",null,1]]}]}]}`,
		},

		{
			name:    "order desc with an aggregate and sparse data",
			params:  url.Values{"db": []string{"db0"}},
			command: `select first("used") AS "used", first("free") AS "free" from "mem" WHERE time >= '2000-01-01T00:00:01Z' AND time <= '2000-01-01T00:00:02Z' GROUP BY host, time(1s) FILL(none) ORDER BY time DESC`,
			exp:     `{"results":[{"statement_id":0,"series":[{"name":"mem","tags":{"host":"server2"},"columns":["time","used","free"],"values":[["2000-01-01T00:00:02Z",4,null],["2000-01-01T00:00:01Z",3,null]]},{"name":"mem","tags":{"host":"server1"},"columns":["time","used","free"],"values":[["2000-01-01T00:00:02Z",null,2],["2000-01-01T00:00:01Z",null,1]]}]}]}`,
		},
	}...)

	ctx := context.Background()
	test.Run(ctx, t, s)
}
