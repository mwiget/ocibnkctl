package aiepp

import (
	"reflect"
	"testing"
)

// inference_epp_stats as the F5 verify-and-monitor page prints it: one row,
// wrapped by tmctl into three stacked header/dashes/value blocks.
const eppStats = `name          schedule_calls schedule_successes schedule_skipped
------------- -------------- ------------------ ----------------
inference_epp              2                  2                0

schedule_fallbacks immediate_responses schedule_retries
------------------ ------------------- ----------------
                 0                   0                0

schedule_retry_successes partial_success schedule_latency_us eos_inject_ok
------------------------ --------------- ------------------- -------------
                       0               0               51000             2
`

func TestParseTmctlRow(t *testing.T) {
	row := parseTmctlRow(eppStats)
	for k, want := range map[string]string{
		"name":                "inference_epp",
		"schedule_calls":      "2",
		"schedule_successes":  "2",
		"schedule_fallbacks":  "0",
		"schedule_latency_us": "51000",
		"eos_inject_ok":       "2",
	} {
		if row[k] != want {
			t.Errorf("row[%q] = %q, want %q", k, row[k], want)
		}
	}
	if got := parseTmctlRow("tmctl: no such table"); len(got) != 0 {
		t.Errorf("parseTmctlRow(error text) = %v, want empty", got)
	}
}

func TestParseNamespaces(t *testing.T) {
	for raw, want := range map[string][]string{
		"":                     nil,
		"  \n":                 nil,
		`["All"]`:              {"All"},
		`["inference","ns-a"]`: {"inference", "ns-a"},
	} {
		got, err := parseNamespaces(raw)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("parseNamespaces(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := parseNamespaces("inference"); err == nil {
		t.Error("parseNamespaces(non-JSON) returned no error")
	}
}

func TestEPPSeries(t *testing.T) {
	// Shape scraped from the BNK 2.4.0 EPP (f5-epp v0.12.0-0.2.0) on lake1.
	const scrape = `# HELP epp_tokenizer_guard_events_total guard events
# TYPE epp_tokenizer_guard_events_total counter
epp_tokenizer_guard_events_total{event="chat_template_naive"} 21
epp_tokenizer_guard_events_total{event="encode_failed"} 20
f5_epp_prefix_index_lookups_total{instance="llm-pool-epp-6c94c57647-677f9",pool="llm-pool",result="miss"} 2
f5_epp_prefix_index_lookups_total{instance="llm-pool-epp-other",pool="llm-pool",result="miss"} 3
f5_epp_prefix_index_lookups_total{instance="llm-pool-epp-6c94c57647-677f9",pool="llm-pool",result="no_query"} 21
f5_epp_prefix_index_size{instance="llm-pool-epp-6c94c57647-677f9",pool="llm-pool"} 0
`
	got := eppSeries(scrape)
	want := map[string]float64{
		`epp_tokenizer_guard_events_total{event="chat_template_naive"}`: 21,
		`epp_tokenizer_guard_events_total{event="encode_failed"}`:       20,
		`f5_epp_prefix_index_lookups_total{result="miss"}`:              5,
		`f5_epp_prefix_index_lookups_total{result="no_query"}`:          21,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("eppSeries =\n%v\nwant\n%v", got, want)
	}
}
