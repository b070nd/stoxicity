package company

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// Awaysis Capital's record, trimmed (read from data.sec.gov on 2026-11-07).
const awcaRecord = `{"cik":"0001021917","entityType":"operating","sic":"6512","sicDescription":"Opeators of  Nonresidential Buildings",
"name":"Awaysis Capital, Inc.","tickers":["AWCA"],"exchanges":["OTC"],"ein":"270514566","lei":null,"description":"","website":"","investorWebsite":"",
"category":"Non-accelerated filer<br>Smaller reporting company","fiscalYearEnd":"0630","stateOfIncorporation":"DE",
"addresses":{"business":{"city":"MIRAMAR","stateOrCountryDescription":"FL"}},"phone":"954-931-9244","formerNames":[]}`

func TestRecordKeepsExchangeAddressAndIdentifiersAndTidiesTheText(t *testing.T) {
	in, err := parseSubmission([]byte(awcaRecord), "0001021917")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Exchanges) != 1 || in.Exchanges[0] != "OTC" || in.City != "MIRAMAR" || in.Region != "FL" ||
		in.Phone != "954-931-9244" || in.EIN != "270514566" || in.LEI != "" {
		t.Errorf("record = %+v", in)
	}
	if in.Description != "Operators of Nonresidential Buildings" {
		t.Errorf("the SEC's typo and double space stay: %q", in.Description)
	}
	if in.Category != "Non-accelerated filer, Smaller reporting company" {
		t.Errorf("category = %q", in.Category)
	}
}

func TestRecordsOfAnOlderRevisionAreFetchedAgainWhateverTheirAge(t *testing.T) {
	dir := t.TempDir()
	if err := saveIndustry(dir, IndustryCache{"0000320193": {CIK: "0000320193", SIC: "3571", FetchedAt: t0}}); err != nil {
		t.Fatal(err) // Rev 0: stored before the exchange and address were kept
	}
	f := httpx.NewFake()
	f.Reply(subAt("0000320193"), resp(appleRecord))
	res, err := Enrich(context.Background(), f, dir, []string{"320193"}, EnrichOptions{URL: subURL, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil || res.Fetched != 1 || res.Skipped != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
	if c, _ := LoadIndustry(dir); c["0000320193"].Rev != industryRev || c["0000320193"].Exchanges[0] != "Nasdaq" {
		t.Errorf("record = %+v", c["0000320193"])
	}
}

func TestEnrichWithWorkersStoresEveryRecordOnce(t *testing.T) {
	f := httpx.NewFake()
	var ciks []string
	for _, c := range []string{"0000000001", "0000000002", "0000000003", "0000000004", "0000000005", "0000000006"} {
		f.Reply(subAt(c), resp(`{"cik":"`+c+`","name":"N","sic":"1000"}`))
		ciks = append(ciks, c)
	}
	dir := t.TempDir()
	res, err := Enrich(context.Background(), f, dir, ciks, EnrichOptions{URL: subURL, Workers: 4})
	if err != nil || res.Fetched != 6 || res.Left != 0 || len(f.Calls()) != 6 {
		t.Fatalf("%+v, %v, %d calls", res, err, len(f.Calls()))
	}
	if c, _ := LoadIndustry(dir); len(c) != 6 {
		t.Errorf("cache has %d records", len(c))
	}
}
