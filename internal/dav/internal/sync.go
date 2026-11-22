package internal

import (
	"encoding/xml"
	"net/http"
)

// Nexus additions: collection sync (RFC 6578), getctag and PROPPATCH helpers.

const CalendarServerNamespace = "http://calendarserver.org/ns/"

var (
	SyncTokenName          = xml.Name{Space: Namespace, Local: "sync-token"}
	SupportedReportSetName = xml.Name{Space: Namespace, Local: "supported-report-set"}
	SyncCollectionName     = xml.Name{Space: Namespace, Local: "sync-collection"}
	GetCTagName            = xml.Name{Space: CalendarServerNamespace, Local: "getctag"}
)

// https://tools.ietf.org/html/rfc6578#section-6.2
type SyncToken struct {
	XMLName xml.Name `xml:"DAV: sync-token"`
	Token   string   `xml:",chardata"`
}

// CalendarServer extension, used by Thunderbird and DAVx⁵ as a cheap
// "has anything changed" check.
type GetCTag struct {
	XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
	CTag    string   `xml:",chardata"`
}

// https://tools.ietf.org/html/rfc3253#section-3.1.5
type SupportedReportSet struct {
	XMLName xml.Name          `xml:"DAV: supported-report-set"`
	Reports []SupportedReport `xml:"supported-report"`
}

type SupportedReport struct {
	XMLName xml.Name   `xml:"DAV: supported-report"`
	Report  ReportType `xml:"report"`
}

type ReportType struct {
	XMLName xml.Name      `xml:"DAV: report"`
	Raw     []RawXMLValue `xml:",any"`
}

func NewSupportedReportSet(names ...xml.Name) *SupportedReportSet {
	set := &SupportedReportSet{}
	for _, n := range names {
		set.Reports = append(set.Reports, SupportedReport{
			Report: ReportType{Raw: []RawXMLValue{*NewRawXMLElement(n, nil, nil)}},
		})
	}
	return set
}

// NewValidSyncTokenError is the 403 + DAV:valid-sync-token precondition
// failure that tells a client to discard its token and resync from scratch.
func NewValidSyncTokenError() error {
	return &HTTPError{
		Code: http.StatusForbidden,
		Err: &Error{Raw: []RawXMLValue{
			*NewRawXMLElement(xml.Name{Space: Namespace, Local: "valid-sync-token"}, nil, nil),
		}},
	}
}

// PropPatchSetter applies one property from a PROPPATCH. raw is nil for a
// DAV:remove instruction.
type PropPatchSetter func(raw *RawXMLValue) error

// ApplyPropPatch runs a PROPPATCH atomically against a fixed set of writable
// properties (RFC 4918 §9.2). If any property is unknown or fails to decode,
// no setter is kept: the failing properties get 403 and the rest 424, and ok
// is false. Otherwise every setter has run, all properties get 200, and the
// caller should commit the accumulated change.
func ApplyPropPatch(path string, update *PropertyUpdate, setters map[xml.Name]PropPatchSetter) (resp *Response, ok bool, err error) {
	type op struct {
		name xml.Name
		raw  *RawXMLValue
	}
	var ops []op
	for i := range update.Remove {
		for j := range update.Remove[i].Prop.Raw {
			raw := &update.Remove[i].Prop.Raw[j]
			if name, ok := raw.XMLName(); ok {
				ops = append(ops, op{name: name})
			}
		}
	}
	for i := range update.Set {
		for j := range update.Set[i].Prop.Raw {
			raw := &update.Set[i].Prop.Raw[j]
			if name, ok := raw.XMLName(); ok {
				ops = append(ops, op{name: name, raw: raw})
			}
		}
	}

	failed := map[xml.Name]bool{}
	for _, o := range ops {
		set, known := setters[o.name]
		if !known || set(o.raw) != nil {
			failed[o.name] = true
		}
	}

	resp = &Response{Hrefs: []Href{{Path: path}}}
	for _, o := range ops {
		code := http.StatusOK
		if failed[o.name] {
			code = http.StatusForbidden
		} else if len(failed) > 0 {
			code = http.StatusFailedDependency
		}
		if err := resp.EncodeProp(code, NewRawXMLElement(o.name, nil, nil)); err != nil {
			return nil, false, err
		}
	}
	return resp, len(failed) == 0, nil
}
