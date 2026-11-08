package instrument

func withCheck(body string) string {
	d, err := ISINCheckDigit(body)
	if err != nil {
		panic(err)
	}
	return body + string(d)
}

// ExampleSet is a small import file that shows every case the model has to get
// right: a US stock, a euro stock, a London stock quoted in pence, a fund with a
// distributing and an accumulating class that share an issuer and trade on two
// venues in two currencies. The fund and its issuer are invented. The real
// companies are here only for their public identifiers; nothing says they are
// recommended or tradable for any account.
func ExampleSet() Set {
	return Set{
		Issuers: []Issuer{
			{ID: "iss_apple", Name: "Apple Inc.", Country: "US", CIK: "0000320193"},
			{ID: "iss_sap", Name: "SAP SE", Country: "DE"},
			{ID: "iss_bae", Name: "BAE Systems plc", Country: "GB"},
			{ID: "iss_example_funds", Name: "Example Funds plc (invented)", Country: "IE"},
		},
		Securities: []Security{
			{ID: "sec_apple", IssuerID: "iss_apple", Name: "Apple Inc. common stock", Kind: KindStock, ShareClass: "common", ISIN: "US0378331005"},
			{ID: "sec_sap", IssuerID: "iss_sap", Name: "SAP SE ordinary shares", Kind: KindStock, ShareClass: "ordinary", ISIN: "DE0007164600"},
			{ID: "sec_bae", IssuerID: "iss_bae", Name: "BAE Systems ordinary shares", Kind: KindStock, ShareClass: "ordinary", ISIN: "GB0002634946"},
			{ID: "sec_exw_dist", IssuerID: "iss_example_funds", Name: "Example World UCITS ETF", Kind: KindETF, ShareClass: "USD Dist",
				ISIN: withCheck("IE00EXAMPL1"), Distribution: Distributing, Domicile: "IE", UCITS: Yes},
			{ID: "sec_exw_acc", IssuerID: "iss_example_funds", Name: "Example World UCITS ETF", Kind: KindETF, ShareClass: "USD Acc",
				ISIN: withCheck("IE00EXAMPL2"), Distribution: Accumulating, Domicile: "IE", UCITS: Yes},
		},
		Listings: []Listing{
			{SecurityID: "sec_apple", MIC: "XNAS", Ticker: "AAPL", Currency: "USD", QuoteUnit: "USD", From: "1980-12-12", Primary: Yes},
			{SecurityID: "sec_sap", MIC: "XETR", Ticker: "SAP", Currency: "EUR", QuoteUnit: "EUR", From: "1995-11-28", Primary: Yes},
			{SecurityID: "sec_bae", MIC: "XLON", Ticker: "BA", Currency: "GBP", QuoteUnit: "GBp", From: "1999-11-30", Primary: Yes},
			{SecurityID: "sec_exw_dist", MIC: "XETR", Ticker: "EXWD", Currency: "EUR", QuoteUnit: "EUR", From: "2020-01-02", Primary: UnknownTri},
			{SecurityID: "sec_exw_dist", MIC: "XLON", Ticker: "EXWD", Currency: "GBP", QuoteUnit: "GBp", From: "2020-01-02", Primary: UnknownTri},
			{SecurityID: "sec_exw_acc", MIC: "XETR", Ticker: "EXWA", Currency: "EUR", QuoteUnit: "EUR", From: "2020-01-02", Primary: UnknownTri},
			{SecurityID: "sec_exw_acc", MIC: "XLON", Ticker: "EXWA", Currency: "USD", QuoteUnit: "USD", From: "2021-06-01", Primary: UnknownTri},
		},
	}
}
