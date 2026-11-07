package edgar

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// TradeReader reads what a Form 4 reports. The filing's complete submission
// text (accession-number.txt in the filing folder) holds the XML document
// inline, so one small request (about 6 KB) reads a filing, whatever the filing
// agent called the XML file; the folder listing would cost a second one.
type TradeReader struct {
	client httpx.Client
	base   string
}

// NewTradeReader returns a reader that makes its requests through client.
func NewTradeReader(client httpx.Client, cfg Config) *TradeReader {
	return &TradeReader{client: client, base: cfg.withDefaults().WebBase}
}

// Trades reads the insider transactions of one Form 4: acc is the accession
// number, cik the issuer (or any company the filing belongs to), filed the day
// it was filed. A filing that reports no transaction in ordinary shares (only
// holdings, options or nothing) gives no trades and no error.
func (r *TradeReader) Trades(ctx context.Context, acc Accession, cik, filed string) ([]model.InsiderTrade, error) {
	if !acc.valid() {
		return nil, permanentError(r.base, 0, fmt.Errorf("edgar: invalid accession number %q", acc))
	}
	url := r.base + "/Archives/edgar/data/" + strings.TrimLeft(cik, "0") + "/" +
		strings.ReplaceAll(string(acc), "-", "") + "/" + string(acc) + ".txt"
	resp, err := r.client.Do(ctx, httpx.Request{URL: url})
	if err != nil {
		return nil, err
	}
	if looksLikeHTML(resp.Body) {
		return nil, blockedError(url, resp.Status)
	}
	trades, err := ParseOwnership(resp.Body)
	if err != nil {
		return nil, permanentError(url, resp.Status, err)
	}
	for i := range trades {
		trades[i].Accession, trades[i].Seq, trades[i].Filed = string(acc), i+1, filed
	}
	return trades, nil
}

// Blocked reports whether err means EDGAR should be left alone for a while
// (it answered with a rate-limit page or a throttling status), as opposed to a
// filing that cannot be read.
func Blocked(err error) bool { return mustStop(err) }

// ownership mirrors the parts of an ownershipDocument that matter. A value in
// the document is written <name><value>text</value></name>; the type below
// reads the inner text.
type ownership struct {
	DocumentType string `xml:"documentType"`
	Period       string `xml:"periodOfReport"`
	Issuer       struct {
		CIK    string `xml:"issuerCik"`
		Symbol string `xml:"issuerTradingSymbol"`
	} `xml:"issuer"`
	Owners []struct {
		ID struct {
			CIK  string `xml:"rptOwnerCik"`
			Name string `xml:"rptOwnerName"`
		} `xml:"reportingOwnerId"`
		Relationship struct {
			Director     string `xml:"isDirector"`
			Officer      string `xml:"isOfficer"`
			TenPercent   string `xml:"isTenPercentOwner"`
			Other        string `xml:"isOther"`
			OfficerTitle string `xml:"officerTitle"`
			OtherText    string `xml:"otherText"`
		} `xml:"reportingOwnerRelationship"`
	} `xml:"reportingOwner"`
	Plan   string `xml:"aff10b5One"`
	Trades []struct {
		Security inner `xml:"securityTitle"`
		Date     inner `xml:"transactionDate"`
		Coding   struct {
			Code string `xml:"transactionCode"`
		} `xml:"transactionCoding"`
		Amounts struct {
			Shares   inner `xml:"transactionShares"`
			Price    inner `xml:"transactionPricePerShare"`
			Acquired inner `xml:"transactionAcquiredDisposedCode"`
		} `xml:"transactionAmounts"`
		Post struct {
			Shares inner `xml:"sharesOwnedFollowingTransaction"`
		} `xml:"postTransactionAmounts"`
		Nature struct {
			Direct inner `xml:"directOrIndirectOwnership"`
		} `xml:"ownershipNature"`
	} `xml:"nonDerivativeTable>nonDerivativeTransaction"`
}

type inner struct {
	Value string `xml:"value"`
}

// ParseOwnership reads the transactions in ordinary shares from a Form 4, given
// either the XML document itself or the filing's submission text that contains
// it. Accession, Seq and Filed are left for the caller to set. An amendment
// (4/A) is read like the form and says so in Form; which earlier trades it
// replaces is decided by insider.Latest from the owner and the period.
func ParseOwnership(body []byte) ([]model.InsiderTrade, error) {
	const start, end = "<ownershipDocument", "</ownershipDocument>"
	if i := bytes.Index(body, []byte(start)); i >= 0 {
		body = body[i:]
		if j := bytes.Index(body, []byte(end)); j >= 0 {
			body = body[:j+len(end)]
		}
	} else {
		return nil, errors.New("edgar: no ownershipDocument in the filing")
	}
	var doc ownership
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("edgar: parse ownership document: %w", err)
	}

	var names, roles []string
	for _, o := range doc.Owners {
		names = append(names, strings.TrimSpace(o.ID.Name))
		roles = appendNew(roles, ownerRoles(o.Relationship.OfficerTitle, o.Relationship.OtherText,
			flag(o.Relationship.Officer), flag(o.Relationship.Director), flag(o.Relationship.TenPercent), flag(o.Relationship.Other))...)
	}
	owner, ownerCIK := strings.Join(names, " / "), ""
	if len(doc.Owners) > 0 {
		ownerCIK = padCIK(strings.TrimSpace(doc.Owners[0].ID.CIK))
	}
	role := strings.Join(roles, ", ")
	cik := padCIK(strings.TrimSpace(doc.Issuer.CIK))
	plan := flag(doc.Plan)

	var out []model.InsiderTrade
	for _, t := range doc.Trades {
		code := strings.ToUpper(strings.TrimSpace(t.Coding.Code))
		shares, ok := number(t.Amounts.Shares.Value)
		if code == "" || !ok {
			continue
		}
		price, _ := number(t.Amounts.Price.Value)
		after, _ := number(t.Post.Shares.Value)
		out = append(out, model.InsiderTrade{
			CIK: cik, Owner: owner, OwnerCIK: ownerCIK, Role: role,
			Security: strings.TrimSpace(t.Security.Value), Form: strings.ToUpper(strings.TrimSpace(doc.DocumentType)),
			Period: strings.TrimSpace(doc.Period),
			Date:   strings.TrimSpace(t.Date.Value), Code: code,
			Acquired: strings.EqualFold(strings.TrimSpace(t.Amounts.Acquired.Value), "A"),
			Shares:   shares, Price: price, After: after,
			Direct: !strings.EqualFold(strings.TrimSpace(t.Nature.Direct.Value), "I"),
			Plan:   plan,
		})
	}
	return out, nil
}

// ownerRoles is what the relationship flags of one reporting person say:
// the officer's own title (or "officer"), director, 10% owner, and the text of
// "other".
func ownerRoles(title, other string, officer, director, tenPercent, isOther bool) []string {
	var out []string
	if officer {
		if t := strings.TrimSpace(title); t != "" {
			out = append(out, t)
		} else {
			out = append(out, "officer")
		}
	}
	if director {
		out = append(out, "director")
	}
	if tenPercent {
		out = append(out, "10% owner")
	}
	if isOther {
		if t := strings.TrimSpace(other); t != "" {
			out = append(out, t)
		} else {
			out = append(out, "other")
		}
	}
	return out
}

func appendNew(list []string, more ...string) []string {
	for _, s := range more {
		if !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	return list
}

// flag reads the 1/0 or true/false the form uses for a yes.
func flag(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true":
		return true
	}
	return false
}

// number reads a share count or a price; false for an empty or unreadable one.
func number(s string) (float64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}
